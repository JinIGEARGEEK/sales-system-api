package apitests

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// contractUploadRequest builds POST /contracts/:id/upload with a small PDF.
func contractUploadRequest(t *testing.T, contractID uint, token string) *http.Request {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, "Signed contract")
	var file bytes.Buffer
	require.NoError(t, pdf.Output(&file))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "signed.pdf")
	require.NoError(t, err)
	_, err = part.Write(file.Bytes())
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/contracts/"+itoa(contractID)+"/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func storedContract(t *testing.T, db *gorm.DB, id uint) models.Contract {
	t.Helper()
	var c models.Contract
	require.NoError(t, db.First(&c, id).Error)
	return c
}

// TestContractSigned_OnlyViaUploadOrWithSignedDocument: Create/PUT can't
// set signed by hand on a contract without a signed file + signed_date;
// Upload signs it; a legacy row that has both may be set signed by PUT.
func TestContractSigned_OnlyViaUploadOrWithSignedDocument(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{"status": "signed"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	draft := seedContract(t, db, deal.ID, models.ContractStatusDraft)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(draft.ID), map[string]interface{}{"status": "signed"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// A file without a signed_date is not enough either.
	url := "/uploads/x.pdf"
	require.NoError(t, db.Model(draft).Update("signed_file_url", url).Error)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(draft.ID), map[string]interface{}{"status": "signed"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	require.NoError(t, db.Model(draft).Update("signed_date", time.Now()).Error)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(draft.ID), map[string]interface{}{"status": "signed"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, models.ContractStatusSigned, storedContract(t, db, draft.ID).Status)

	sent := seedContract(t, db, deal.ID, models.ContractStatusSent)
	var out struct {
		Data models.Contract `json:"data"`
	}
	resp = doJSON(t, app, contractUploadRequest(t, sent.ID, testutil.Token(t, admin.ID, admin.Role)), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, models.ContractStatusSigned, out.Data.Status)
	assert.NotNil(t, out.Data.SignedFileURL)
	assert.NotNil(t, out.Data.SignedDate)
}

// TestContractSigned_IsLocked: a stored-signed contract takes no status,
// quote_id or end_date change and no second signed upload; resending its
// stored values is fine. A past end_date (displayed as expired elsewhere)
// doesn't unlock it.
func TestContractSigned_IsLocked(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	quote := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusAccepted, "QT-CL")
	otherQuote := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusDraft, "QT-CL2")

	contract := seedContract(t, db, deal.ID, models.ContractStatusSent)
	end := "2020-06-30"
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), map[string]interface{}{
		"quote_id": quote.ID, "end_date": end,
	}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	token := testutil.Token(t, admin.ID, admin.Role)
	resp = doJSON(t, app, contractUploadRequest(t, contract.ID, token), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	for _, body := range []map[string]interface{}{
		{"status": "draft"},
		{"status": "sent"},
		{"status": "expired"},
		{"quote_id": otherQuote.ID},
		{"end_date": "2027-12-31"},
		{"end_date": nil},
	} {
		resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), body, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusConflict, resp.StatusCode, "%v", body)
	}
	resp = doJSON(t, app, contractUploadRequest(t, contract.ID, token), nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode, "a second signed upload")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), map[string]interface{}{
		"status": "signed", "quote_id": quote.ID, "end_date": end,
	}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "resending the stored values")

	stored := storedContract(t, db, contract.ID)
	assert.Equal(t, models.ContractStatusSigned, stored.Status)
	require.NotNil(t, stored.QuoteID)
	assert.Equal(t, quote.ID, *stored.QuoteID)
	require.NotNil(t, stored.EndDate)
	assert.Equal(t, end, stored.EndDate.Format("2006-01-02"))
}

// TestContractStatusChangesAreAudited covers PUT and Upload.
func TestContractStatusChangesAreAudited(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	contract := seedContract(t, db, deal.ID, models.ContractStatusDraft)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), map[string]interface{}{"status": "sent"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), map[string]interface{}{"end_date": "2027-01-01"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = doJSON(t, app, contractUploadRequest(t, contract.ID, testutil.Token(t, admin.ID, admin.Role)), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ?", "contract", contract.ID).Order("id").Find(&entries).Error)
	require.Len(t, entries, 2)
	assert.Equal(t, "status_changed", entries[0].Action)
	assert.Equal(t, "draft", entries[0].Before["status"])
	assert.Equal(t, "sent", entries[0].After["status"])
	assert.Equal(t, "sent", entries[1].Before["status"])
	assert.Equal(t, "signed", entries[1].After["status"])
}

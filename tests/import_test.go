package apitests

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// csvImportRequest builds a POST request uploading csvContent as the `file`
// multipart field, matching what ImportCompanies/ImportContacts expect.
func csvImportRequest(t *testing.T, path, csvContent, token string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "import.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte(csvContent))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// TestImportCompanies_DedupesWithinSameFile proves the batched rewrite still
// dedupes two rows in the *same* file that resolve to the same company (here,
// by domain) — the second row must update the company the first row just
// created, not create a duplicate. This is the case an in-memory-map
// preloaded-once-per-import approach could easily get wrong if it only
// checked the DB state from before the request started.
func TestImportCompanies_DedupesWithinSameFile(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)

	csvContent := "name,industry,size,website\n" +
		"Acme Co,Tech,Small,https://acme.com\n" +
		"Acme Corp,Tech,Medium,https://www.acme.com/about\n"

	req := csvImportRequest(t, "/api/v1/companies/import", csvContent, token)
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, body.Data.Created, "first row creates the company")
	assert.Equal(t, 1, body.Data.Updated, "second row (same domain) updates it instead of creating a duplicate")

	var count int64
	db.Model(&models.Company{}).Where("domain = ?", "acme.com").Count(&count)
	assert.Equal(t, int64(1), count, "exactly one company should exist for this domain")

	var company models.Company
	require.NoError(t, db.Where("domain = ?", "acme.com").First(&company).Error)
	assert.Equal(t, "Medium", company.Size, "the later row's data should have won")
}

// TestImportCompanies_UpdatesExistingRow proves a row matching a company
// that already existed before the import (not just one created earlier in
// the same file) is still found via the preloaded index and updated in place.
func TestImportCompanies_UpdatesExistingRow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)

	// Domain is set explicitly, matching how CompanyHandler.Create/Update
	// always populate it (utils.ExtractDomain) — the column is what the
	// import's domain lookup actually matches against, same as it was before
	// this batching rewrite.
	existing := &models.Company{Name: "Beta Inc", Website: "https://beta.com", Domain: "beta.com", Status: models.StatusActive}
	require.NoError(t, db.Create(existing).Error)

	csvContent := "name,industry,size,website\nBeta Incorporated,Finance,Large,https://beta.com\n"
	req := csvImportRequest(t, "/api/v1/companies/import", csvContent, token)
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, body.Data.Created)
	assert.Equal(t, 1, body.Data.Updated)

	var reloaded models.Company
	require.NoError(t, db.First(&reloaded, existing.ID).Error)
	assert.Equal(t, "Finance", reloaded.Industry)
}

// TestImportContacts_DedupesByEmailWithinSameFile mirrors the companies test
// for the email-keyed contact import path.
func TestImportContacts_DedupesByEmailWithinSameFile(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	company := seedCompany(t, db)

	csvContent := "company_id,name,email,phone,role_title\n" +
		itoa(company.ID) + ",Jane Doe,jane@example.com,111,Manager\n" +
		itoa(company.ID) + ",Jane D.,jane@example.com,222,Director\n"

	req := csvImportRequest(t, "/api/v1/contacts/import", csvContent, token)
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, body.Data.Created)
	assert.Equal(t, 1, body.Data.Updated)

	var count int64
	db.Model(&models.Contact{}).Where("email = ?", "jane@example.com").Count(&count)
	assert.Equal(t, int64(1), count)
}

// TestImportCompanies_RejectsOversizedRowCount guards the new maxImportRows
// cap — a file with more rows than the limit must be rejected outright
// rather than processed (which used to mean an unbounded number of
// sequential DB round trips inside one request).
func TestImportCompanies_RejectsOversizedRowCount(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)

	var buf bytes.Buffer
	buf.WriteString("name,industry,size,website\n")
	for i := 0; i < 5001; i++ {
		buf.WriteString("Company,Tech,Small,\n")
	}

	req := csvImportRequest(t, "/api/v1/companies/import", buf.String(), token)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

package apitests

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// multipartQuoteUploadRequest builds a POST /deals/:dealId/quotes/upload
// request uploading a real file — mirrors multipartAttachmentRequest
// (upload_serving_test.go), just against the Quote upload route instead of
// /attachments, and with no extra form fields (Upload only reads "file").
func multipartQuoteUploadRequest(t *testing.T, dealID uint, filename string, content []byte, token string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/deals/"+itoa(dealID)+"/quotes/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestQuoteUpload_NonFlowAccountPDF_StillUploadsWithExtractionFailed guards
// the Upload handler's extraction wiring (internal/utils/flowaccount_extract.go)
// against regressing the upload path itself: a PDF that opens fine but isn't
// a FlowAccount quotation export (no "ใบเสนอราคา"/"เลขที่" markers) must still
// create the Quote exactly as before this feature existed — file attached,
// items empty — with ExtractionStatus recording "failed" rather than nil, so
// the frontend's review banner (extraction_status: 'failed') only ever shows
// up for a Quote actually created via this Upload path. A real FlowAccount
// export's happy path is covered directly against fixture text in
// internal/utils/flowaccount_extract_test.go — this repo has no bundled Thai
// font to render one of those through fpdf for a true end-to-end test.
func TestQuoteUpload_NonFlowAccountPDF_StillUploadsWithExtractionFailed(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	deal := seedDeal(t, db, nil)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, "Not a FlowAccount export")
	var buf bytes.Buffer
	require.NoError(t, pdf.Output(&buf))

	req := multipartQuoteUploadRequest(t, deal.ID, "quote.pdf", buf.Bytes(), token)
	var out quoteEnvelope
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	require.NotNil(t, out.Data.FileURL)
	require.NotNil(t, out.Data.ExtractionStatus)
	assert.Equal(t, "failed", *out.Data.ExtractionStatus)
	assert.Empty(t, out.Data.Items)
	// The Upload handler's own pre-extraction defaults (VatEnabled: true,
	// PriceType: excl_tax) must survive untouched on the "failed" path —
	// only a successful/partial extraction should ever override them.
	assert.True(t, out.Data.VatEnabled)
	assert.Equal(t, models.QuotePriceTypeExclTax, out.Data.PriceType)
}

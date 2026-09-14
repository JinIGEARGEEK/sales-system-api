package apitests

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestQuoteUpload_RejectsContentTypeMismatch guards the content-sniffing fix
// in internal/utils/storage.go (validateContentSniff): a file whose actual
// bytes don't match what its extension claims — an HTML payload renamed to
// "quote.pdf" here — must be rejected (400) rather than trusting the
// filename/extension alone, which would otherwise let a caller stash
// arbitrary mislabeled content behind a trusted-looking extension.
func TestQuoteUpload_RejectsContentTypeMismatch(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	deal := seedDeal(t, db, nil)

	htmlDisguisedAsPDF := []byte("<html><body><script>alert(1)</script></body></html>")
	req := multipartQuoteUploadRequest(t, deal.ID, "quote.pdf", htmlDisguisedAsPDF, token)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestQuoteUpload_AllowsRealPDF is the companion happy-path check: a real
// PDF named "quote.pdf" must still upload successfully — the sniff check
// isn't rejecting valid files of the type they claim to be.
func TestQuoteUpload_AllowsRealPDF(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	deal := seedDeal(t, db, nil)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, "A real PDF")
	var buf bytes.Buffer
	require.NoError(t, pdf.Output(&buf))

	req := multipartQuoteUploadRequest(t, deal.ID, "quote.pdf", buf.Bytes(), token)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

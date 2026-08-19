package apitests

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// multipartAttachmentRequest builds a POST /attachments multipart request
// uploading a real file (not the external_url path the rest of the suite
// exercises), with the given filename/content.
func multipartAttachmentRequest(t *testing.T, companyID uint, filename string, content []byte, token string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("related_type", "company"))
	require.NoError(t, w.WriteField("related_id", itoa(companyID)))
	require.NoError(t, w.WriteField("category", "Proposal"))
	part, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/attachments", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// TestUploadedFile_IsServedAndAuthGated guards two previously-broken
// behaviors: SaveUpload's returned /uploads/<name> URL used to be a dead link
// (nothing served that path at all, on any deployment), and once served it
// must still require auth like every other business-document endpoint.
func TestUploadedFile_IsServedAndAuthGated(t *testing.T) {
	app, db := testutil.App(t)
	company := seedCompany(t, db)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	token := testutil.Token(t, rep.ID, rep.Role)

	content := []byte("%PDF-1.4 fake pdf content for test")
	req := multipartAttachmentRequest(t, company.ID, "proposal.pdf", content, token)

	var created struct {
		Data models.Attachment `json:"data"`
	}
	resp := doJSON(t, app, req, &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, created.Data.FileURL, "expected SaveUpload to set file_url")
	// utils.SaveUpload returns a root-level "/uploads/<name>" path (not under
	// /api/v1), matching how routes.go registers the static handler.
	fileURL := *created.Data.FileURL

	t.Run("unauthenticated request is rejected", func(t *testing.T) {
		getReq := httptest.NewRequest(http.MethodGet, fileURL, nil)
		getResp, err := app.Test(getReq, -1)
		require.NoError(t, err)
		defer getResp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, getResp.StatusCode)
	})

	t.Run("authenticated request serves the uploaded content", func(t *testing.T) {
		getReq := testutil.AuthRequest(t, http.MethodGet, fileURL, nil, rep.ID, rep.Role)
		getResp, err := app.Test(getReq, -1)
		require.NoError(t, err)
		defer getResp.Body.Close()
		require.Equal(t, http.StatusOK, getResp.StatusCode)
		body, err := io.ReadAll(getResp.Body)
		require.NoError(t, err)
		assert.Equal(t, content, body)
	})
}

// TestUpload_RejectsUnsupportedFileType guards the new extension allow-list —
// previously any extension (.html, .svg, .exe) was accepted and, once served,
// an .html/.svg upload would be a stored-XSS vector from this app's own origin.
func TestUpload_RejectsUnsupportedFileType(t *testing.T) {
	app, db := testutil.App(t)
	company := seedCompany(t, db)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	token := testutil.Token(t, rep.ID, rep.Role)

	req := multipartAttachmentRequest(t, company.ID, "payload.html", []byte("<script>alert(1)</script>"), token)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

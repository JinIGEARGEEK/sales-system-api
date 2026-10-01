package apitests

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/clientip"
	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/server"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// These cover the app-level config server.New sets up (and testutil.App now
// shares with production), not any one handler.

// largePDF is a well-formed PDF of at least minSize bytes: one page holding
// a random-noise image, which no compression shrinks — so it passes the
// upload's content sniff (and its PDF extraction) while being big enough to
// exercise the body limit.
func largePDF(t *testing.T, minSize int) []byte {
	t.Helper()
	side := 1
	for side*side*3 < minSize {
		side += 64
	}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	rng := rand.New(rand.NewSource(1))
	_, _ = rng.Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff // opaque: an alpha channel would become a second (soft-mask) image
	}
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, img))

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.RegisterImageOptionsReader("noise", fpdf.ImageOptions{ImageType: "PNG"}, &pngBuf)
	pdf.ImageOptions("noise", 10, 10, 100, 100, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	var buf bytes.Buffer
	require.NoError(t, pdf.Output(&buf))
	require.GreaterOrEqual(t, buf.Len(), minSize)
	return buf.Bytes()
}

// Fiber's default 4MB BodyLimit used to cut off any upload over 4MB with a
// bare 413, so utils.MaxUploadSize (10MB) was unreachable.
func TestBodyLimit_AcceptsUploadAboveFiberDefault(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := multipartQuoteUploadRequest(t, deal.ID, "quote.pdf", largePDF(t, 6<<20), testutil.Token(t, admin.ID, admin.Role))
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode, "a 6MB upload must reach the handler")
}

// The limit leaves room above MaxUploadSize for the multipart envelope, so
// a file at the handler's own limit isn't cut off by the server first.
// (An over-limit body can't be driven through app.Test: fasthttp answers
// 413 and drops the connection, which app.Test surfaces as an error.)
func TestBodyLimit_LeavesHeadroomAboveMaxUploadSize(t *testing.T) {
	app, _ := testutil.App(t)
	assert.Equal(t, server.BodyLimit, app.Config().BodyLimit)
	assert.Greater(t, app.Config().BodyLimit, utils.MaxUploadSize)
}

// A panicking handler is recovered and answered with the generic JSON 500 —
// the panic value (which could be anything, including internal detail)
// never reaches the client.
func TestPanic_ReturnsJSON500(t *testing.T) {
	app, _ := testutil.App(t)
	app.Get("/test-only/panic", func(*fiber.Ctx) error { panic("boom: secret internal detail") })

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	resp := doJSON(t, app, httptest.NewRequest(http.MethodGet, "/test-only/panic", nil), &body)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "INTERNAL_ERROR", body.Error.Code)
	assert.Equal(t, "Internal server error", body.Error.Message)
	assert.NotEmpty(t, resp.Header.Get("X-Request-ID"), "requestid still runs, so the log line can be correlated")
}

func TestHealth_OKWithDatabase(t *testing.T) {
	app, _ := testutil.App(t)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/health", nil), -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// With the database unreachable /health reports 503, so the platform doesn't
// route traffic to an instance that would 500 every real request.
func TestHealth_503WhenDatabaseUnreachable(t *testing.T) {
	cfg := testutil.Config()
	db, err := database.Connect(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	app := server.New(cfg, db, utils.NewMemoryStorage(), &clientip.Resolver{}, false)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/health", nil), -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(raw), "SERVICE_UNAVAILABLE")
}

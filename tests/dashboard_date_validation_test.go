package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDashboardSummary_RejectsMalformedDateParams guards that an invalid
// date_from/date_to now 400s instead of reaching Postgres as a raw query
// bound — previously a malformed value failed each aggregate query at the
// driver level (Summary's helpers discard Scan's error return), silently
// degrading the whole dashboard to zeroed-out figures instead of surfacing
// the bad input to the caller.
func TestDashboardSummary_RejectsMalformedDateParams(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	t.Run("malformed date_from", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary?date_from=not-a-date", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	})

	t.Run("malformed date_to", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary?date_to=2026-13-99", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	})

	t.Run("valid date range still succeeds", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary?date_from=2026-01-01&date_to=2026-12-31", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

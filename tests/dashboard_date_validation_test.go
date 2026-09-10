package apitests

import (
	"net/http"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestDashboardSummary_DateRangeWithCompanyTagFilter guards against
// baseFilter's "column reference created_at is ambiguous" regression: once a
// date_from/date_to filter is combined with company_tag, baseFilter joins
// companies (which also has a created_at column via AuditedModel), so an
// unqualified "created_at" in the date WHERE clause becomes ambiguous to
// Postgres and the whole aggregate query fails.
func TestDashboardSummary_DateRangeWithCompanyTagFilter(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Company{}).Where("id = ?", deal.CompanyID).
		Update("tags", pq.StringArray{"vip"}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary?date_from=2026-01-01&date_to=2026-12-31&company_tag=vip", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			OpenPipelineValue float64 `json:"open_pipeline_value"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// Asserting the real aggregate (not just a 200) matters here: the
	// ambiguous-column error this guards against was previously swallowed by
	// Summary's discarded Scan error, silently zeroing every figure instead
	// of failing the request.
	assert.Equal(t, float64(1000), out.Data.OpenPipelineValue)
}

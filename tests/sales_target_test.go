package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestSalesTarget_RBAC guards /admin/sales-targets' Admin-only gate (matches
// PipelineStage/LeadSource's adminOnly pattern) — a Sales Manager, despite
// having broader read access elsewhere in the system, must not manage
// per-quarter targets.
func TestSalesTarget_RBAC(t *testing.T) {
	app, db := testutil.App(t)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	t.Run("manager forbidden", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/sales-targets", nil, manager.ID, manager.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("admin ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/sales-targets", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// TestSalesTargetCreate_ValidatesFields guards salesTargetForm.validate's
// three checks: quarter in [1,4], a plausible year, and a non-negative
// required target_value.
func TestSalesTargetCreate_ValidatesFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"quarter too low", map[string]interface{}{"year": 2026, "quarter": 0, "target_value": 1000}},
		{"quarter too high", map[string]interface{}{"year": 2026, "quarter": 5, "target_value": 1000}},
		{"year out of range", map[string]interface{}{"year": 1999, "quarter": 1, "target_value": 1000}},
		{"missing target_value", map[string]interface{}{"year": 2026, "quarter": 1}},
		{"negative target_value", map[string]interface{}{"year": 2026, "quarter": 1, "target_value": -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets", tc.body, admin.ID, admin.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		})
	}
}

// TestSalesTargetCreate_RejectsDuplicatePeriod guards the one-row-per-(year,
// quarter) uniqueness check — a second Create for an already-targeted period
// must 422 pointing the caller at PATCHing the existing row instead.
// Cleans up its own row: sales_targets, like app_settings, is never
// truncated between tests (or even between separate test-binary runs against
// the same persistent test DB) — an arbitrary far-future year avoids
// colliding with the "current quarter" tests below, but a second run of this
// same test without cleanup would collide with itself.
func TestSalesTargetCreate_RejectsDuplicatePeriod(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	t.Cleanup(func() { db.Unscoped().Where("year = ? AND quarter = ?", 2031, 2).Delete(&models.SalesTarget{}) })

	body := map[string]interface{}{"year": 2031, "quarter": 2, "target_value": 4000000}
	first := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets", body, admin.ID, admin.Role)
	require.Equal(t, http.StatusCreated, doJSON(t, app, first, nil).StatusCode)

	second := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets", body, admin.ID, admin.Role)
	resp := doJSON(t, app, second, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestSalesTargetUpdate_RejectsCollidingPeriod guards the same uniqueness
// check on Update: moving an existing row's (year, quarter) onto another
// row's period must 422 rather than silently creating two rows for one
// period. Cleans up both rows it creates — see the comment on
// TestSalesTargetCreate_RejectsDuplicatePeriod above for why.
func TestSalesTargetUpdate_RejectsCollidingPeriod(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	t.Cleanup(func() { db.Unscoped().Where("year = ?", 2032).Delete(&models.SalesTarget{}) })

	var q1, q2 models.SalesTarget
	require.Equal(t, http.StatusCreated, doJSON(t, app,
		testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets",
			map[string]interface{}{"year": 2032, "quarter": 1, "target_value": 1000000}, admin.ID, admin.Role),
		&struct{ Data *models.SalesTarget }{&q1}).StatusCode)
	require.Equal(t, http.StatusCreated, doJSON(t, app,
		testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets",
			map[string]interface{}{"year": 2032, "quarter": 2, "target_value": 2000000}, admin.ID, admin.Role),
		&struct{ Data *models.SalesTarget }{&q2}).StatusCode)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/sales-targets/"+itoa(q2.ID), map[string]interface{}{
		"year": 2032, "quarter": 1, "target_value": 2000000,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestSalesTargetCreate_WritesAuditLogAndInvalidatesDashboardCache guards two
// things a plain db.Create() wouldn't give for free: an audit-log entry
// (entity_type=sales_target, action=created), and — since a SalesTarget row
// changes what GET /dashboard/summary's pipeline_coverage_ratio resolves to
// for its period without touching the deals table at all — that Summary's
// response cache gets invalidated so the change is visible immediately.
//
// Cleans up its own row at the end: sales_targets, like app_settings, is
// never truncated between tests, and this test — needing to exercise the
// real current (year, quarter) currentQuarterTarget resolves against — can't
// dodge collisions with other current-quarter tests the way
// TestSalesTargetCreate_RejectsDuplicatePeriod does (picking an arbitrary
// far-future year instead).
func TestSalesTargetCreate_WritesAuditLogAndInvalidatesDashboardCache(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	now := time.Now()
	quarter := (int(now.Month())-1)/3 + 1

	// Populate the dashboard cache with the pre-override target for the
	// current quarter before creating an override for it.
	summaryReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, summaryReq, nil).StatusCode)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets", map[string]interface{}{
		"year": now.Year(), "quarter": quarter, "target_value": 42000000,
	}, admin.ID, admin.Role)
	var created struct {
		Data models.SalesTarget `json:"data"`
	}
	resp := doJSON(t, app, req, &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	t.Cleanup(func() { db.Unscoped().Delete(&created.Data) })

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "sales_target", created.Data.ID, "created").
		Find(&entries).Error)
	require.Len(t, entries, 1)
	assert.Equal(t, admin.ID, entries[0].ActorID)

	var out struct {
		Data struct {
			QuarterlySalesTarget float64 `json:"quarterly_sales_target"`
		} `json:"data"`
	}
	resp2 := doJSON(t, app, summaryReq, &out)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Equal(t, 42000000.0, out.Data.QuarterlySalesTarget,
		"dashboard must reflect the new per-quarter override immediately, not a cached pre-Create value")
}

// TestSalesTargetDelete_RevertsToFlatFallback guards Delete's documented
// behavior: removing an override reverts pipeline_coverage_ratio's target
// back to AppSettings.QuarterlySalesTarget/4 for that period, and the
// dashboard cache reflects that reversion immediately too.
func TestSalesTargetDelete_RevertsToFlatFallback(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	// Known baseline for the flat fallback (app_settings is a singleton row
	// not truncated between tests — see settings_test.go's comment on this).
	patchSettings := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 8000000, "annual_revenue_goal": 20000000,
	}, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, patchSettings, nil).StatusCode)

	now := time.Now()
	quarter := (int(now.Month())-1)/3 + 1

	var created struct {
		Data models.SalesTarget `json:"data"`
	}
	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/sales-targets", map[string]interface{}{
		"year": now.Year(), "quarter": quarter, "target_value": 99000000,
	}, admin.ID, admin.Role)
	require.Equal(t, http.StatusCreated, doJSON(t, app, createReq, &created).StatusCode)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/admin/sales-targets/"+itoa(created.Data.ID), nil, admin.ID, admin.Role)
	resp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var out struct {
		Data struct {
			QuarterlySalesTarget float64 `json:"quarterly_sales_target"`
		} `json:"data"`
	}
	summaryResp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, summaryResp.StatusCode)
	assert.Equal(t, 2000000.0, out.Data.QuarterlySalesTarget, "8,000,000 / 4 flat fallback, override deleted")
}

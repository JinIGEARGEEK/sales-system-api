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

// TestSettingsUpdate_RequiresBothFields guards PATCH /admin/settings' "both
// fields required on every PATCH" convention (settings.go's settingsForm) —
// a body missing either quarterly_sales_target or annual_revenue_goal must
// 422, not silently zero out the omitted field.
func TestSettingsUpdate_RequiresBothFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 5000000,
		// annual_revenue_goal deliberately omitted
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	req2 := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"annual_revenue_goal": 20000000,
		// quarterly_sales_target deliberately omitted
	}, admin.ID, admin.Role)
	resp2 := doJSON(t, app, req2, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)
}

// TestSettingsUpdate_RejectsNegativeValues guards the ">= 0" validation on
// both fields.
func TestSettingsUpdate_RejectsNegativeValues(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": -1,
		"annual_revenue_goal":    20000000,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	req2 := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 5000000,
		"annual_revenue_goal":    -1,
	}, admin.ID, admin.Role)
	resp2 := doJSON(t, app, req2, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)
}

// TestSettingsUpdate_WritesAuditLog guards against a missing audit trail on
// PATCH /admin/settings: a real change to either the quarterly quota or the
// annual goal must produce an audit_log_entries row (entity_type=settings,
// action=updated) recording the before/after values and the acting Admin —
// this is the one Admin-configurable resource that previously saved with a
// plain db.Save(), unlike deals/projects/products' SaveWithAudit pattern.
func TestSettingsUpdate_WritesAuditLog(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 5000000,
		"annual_revenue_goal":    20000000,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "settings", 1, "updated").Find(&entries).Error)
	require.Len(t, entries, 1, "expected exactly one settings-update audit log entry")
	assert.Equal(t, admin.ID, entries[0].ActorID)
	assert.EqualValues(t, 20000000, entries[0].After["annual_revenue_goal"])
}

// TestSettingsUpdate_NoAuditLogWhenUnchanged guards the "only audit a real
// change" half of that same conditional (mirrors deals.go's stage_changed
// entry, which is also conditional on oldStage != newStage) — PATCHing with
// the exact same values that are already stored must not write a second
// entry. Note app_settings is a singleton row that (like PipelineStage/
// LeadSourceOption) is never truncated between tests, so this deliberately
// sets its own known baseline via a first PATCH rather than assuming
// whatever DefaultAppSettings/a prior test in this file happened to leave
// behind.
func TestSettingsUpdate_NoAuditLogWhenUnchanged(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	body := map[string]interface{}{
		"quarterly_sales_target": 7000000,
		"annual_revenue_goal":    15000000,
	}
	baseline := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", body, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, baseline, nil).StatusCode)

	noop := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", body, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, noop, nil).StatusCode)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ?", "settings", 1).Find(&entries).Error)
	require.Len(t, entries, 1, "the baseline PATCH should audit, the identical follow-up PATCH should not")
}

// TestSettingsUpdate_SetsUpdatedAt guards the new updated_at column (surfaced
// in the Admin config UI as a "last updated" hint) actually advancing on a
// real save.
func TestSettingsUpdate_SetsUpdatedAt(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	before := time.Now().Add(-time.Second)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 5000000,
		"annual_revenue_goal":    20000000,
	}, admin.ID, admin.Role)
	var out struct {
		Data struct {
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, out.Data.UpdatedAt.After(before), "updated_at must advance past the PATCH")
}

// TestDashboardSummary_AnnualRevenueProgressRatio guards
// annual_revenue_progress_ratio's derivation (annual_revenue_actual ÷
// annual_revenue_goal) and that annual_revenue_actual only counts Won Deal
// value from the current calendar year — a Won Deal backdated to last year
// must not count toward this year's goal.
func TestDashboardSummary_AnnualRevenueProgressRatio(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	require.NoError(t, db.Model(&models.AppSettings{}).Where("id = 1").
		Update("annual_revenue_goal", 1000000).Error)

	thisYearDeal := seedDeal(t, db, nil)
	thisYearDeal.Status = models.DealStatusWon
	thisYearDeal.Value = 400000
	require.NoError(t, db.Save(thisYearDeal).Error)

	lastYearDeal := seedDeal(t, db, nil)
	lastYearDeal.Status = models.DealStatusWon
	lastYearDeal.Value = 999999
	require.NoError(t, db.Save(lastYearDeal).Error)
	lastYear := time.Now().AddDate(-1, 0, 0)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", lastYearDeal.ID).
		Update("created_at", lastYear).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			AnnualRevenueGoal          float64 `json:"annual_revenue_goal"`
			AnnualRevenueActual        float64 `json:"annual_revenue_actual"`
			AnnualRevenueProgressRatio float64 `json:"annual_revenue_progress_ratio"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 1000000.0, out.Data.AnnualRevenueGoal)
	assert.Equal(t, 400000.0, out.Data.AnnualRevenueActual, "last year's Won deal must not count toward this year's actual")
	assert.InDelta(t, 0.4, out.Data.AnnualRevenueProgressRatio, 0.0001)
}

// TestDashboardSummary_AnnualRevenueTrendCumulatesByMonth guards
// annualRevenueTrend's cumulative-by-month shape: each point's Actual is the
// running total through that month (not that month's own delta), and
// GoalPace is a straight-line annualGoal * monthsElapsed/12.
func TestDashboardSummary_AnnualRevenueTrendCumulatesByMonth(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	require.NoError(t, db.Model(&models.AppSettings{}).Where("id = 1").
		Update("annual_revenue_goal", 1200000).Error) // 100000/month straight-line pace

	deal := seedDeal(t, db, nil)
	deal.Status = models.DealStatusWon
	deal.Value = 300000
	require.NoError(t, db.Save(deal).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			AnnualRevenueTrend []struct {
				Label    string  `json:"label"`
				Actual   float64 `json:"actual"`
				GoalPace float64 `json:"goal_pace"`
			} `json:"annual_revenue_trend"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	now := time.Now()
	require.Len(t, out.Data.AnnualRevenueTrend, int(now.Month()), "one point per elapsed month, Jan through the current month")

	last := out.Data.AnnualRevenueTrend[len(out.Data.AnnualRevenueTrend)-1]
	assert.Equal(t, now.Format("Jan"), last.Label)
	assert.Equal(t, 300000.0, last.Actual, "cumulative actual through the current month")
	assert.InDelta(t, 100000.0*float64(now.Month()), last.GoalPace, 0.01)
}

// TestSettingsUpdate_InvalidatesDashboardCache guards against Summary's 30s
// response cache (dashboard.go's summaryCache) serving a stale
// quarterly_sales_target/annual_revenue_goal after an Admin changes them via
// the real API — unlike a Deal/Company write, a settings PATCH never touches
// the deals table, so nothing else would invalidate that cache for these two
// fields specifically without SettingsHandler.Update calling
// InvalidateDashboardCache itself.
//
// app_settings is a singleton row that (like PipelineStage/LeadSourceOption)
// is never truncated between tests — mirrors
// TestSettingsUpdate_NoAuditLogWhenUnchanged's approach of establishing its
// own known baseline via a first PATCH rather than assuming
// DefaultAppSettings/a prior test's value is still in place.
func TestSettingsUpdate_InvalidatesDashboardCache(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	getGoal := func() float64 {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
		var out struct {
			Data struct {
				AnnualRevenueGoal float64 `json:"annual_revenue_goal"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		return out.Data.AnnualRevenueGoal
	}
	patchGoal := func(goal int) {
		req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
			"quarterly_sales_target": 5000000,
			"annual_revenue_goal":    goal,
		}, admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
	}

	// Establish a known baseline and populate the cache with it.
	patchGoal(11000000)
	require.Equal(t, 11000000.0, getGoal())

	patchGoal(99000000)
	assert.Equal(t, 99000000.0, getGoal(), "dashboard summary must reflect the new goal immediately, not the cached pre-PATCH value")
}

package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDashboardSummary_UpsellOpportunities guards the upsell_opportunities
// widget: a flat, most-stale-first list of active Companies whose
// last_activity_at (company-scoped Activities only) is NULL (never
// contacted) or at least ?upsell_min_stale_days old (default 60 when
// omitted), excluding archived companies. **Updated 2026-09-09**: this used
// to always return exactly 3 fixed 60/90/120-day tier groups; replaced by a
// single minStaleDays threshold now that the widget shows one filtered list
// instead of three fixed columns (the frontend's own filter dropdown).
func TestDashboardSummary_UpsellOpportunities(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	neverContacted := seedCompany(t, db)

	stale70 := seedCompany(t, db)
	seedCompanyActivity(t, db, stale70.ID, time.Now().AddDate(0, 0, -70))

	stale100 := seedCompany(t, db)
	seedCompanyActivity(t, db, stale100.ID, time.Now().AddDate(0, 0, -100))

	stale150 := seedCompany(t, db)
	seedCompanyActivity(t, db, stale150.ID, time.Now().AddDate(0, 0, -150))

	recentCompany := seedCompany(t, db)
	seedCompanyActivity(t, db, recentCompany.ID, time.Now().AddDate(0, 0, -5))

	archivedStale := seedCompany(t, db)
	require.NoError(t, db.Model(&models.Company{}).Where("id = ?", archivedStale.ID).Update("status", models.StatusArchived).Error)

	type upsellCompany struct {
		ID uint `json:"id"`
	}
	fetchIDs := func(query string) []uint {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary"+query, nil, admin.ID, admin.Role)
		var out struct {
			Data struct {
				UpsellOpportunities []upsellCompany `json:"upsell_opportunities"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		ids := make([]uint, len(out.Data.UpsellOpportunities))
		for i, c := range out.Data.UpsellOpportunities {
			ids[i] = c.ID
		}
		return ids
	}

	t.Run("default (no param) matches the old 60-day tier1 cutoff", func(t *testing.T) {
		ids := fetchIDs("")
		assert.Contains(t, ids, stale70.ID)
		assert.Contains(t, ids, stale100.ID)
		assert.Contains(t, ids, stale150.ID)
		assert.Contains(t, ids, neverContacted.ID, "never-contacted must always qualify")
		assert.NotContains(t, ids, recentCompany.ID)
		assert.NotContains(t, ids, archivedStale.ID)
	})

	t.Run("upsell_min_stale_days=90 excludes the 70-day-stale company", func(t *testing.T) {
		ids := fetchIDs("?upsell_min_stale_days=90")
		assert.NotContains(t, ids, stale70.ID)
		assert.Contains(t, ids, stale100.ID)
		assert.Contains(t, ids, stale150.ID)
		assert.Contains(t, ids, neverContacted.ID)
	})

	t.Run("upsell_min_stale_days=120 only the 150-day-stale and never-contacted qualify", func(t *testing.T) {
		ids := fetchIDs("?upsell_min_stale_days=120")
		assert.NotContains(t, ids, stale70.ID)
		assert.NotContains(t, ids, stale100.ID)
		assert.Contains(t, ids, stale150.ID)
		assert.Contains(t, ids, neverContacted.ID)
	})
}

// seedCompanyActivity creates a company-scoped Activity and backdates its
// created_at, mirroring the existing pattern for backdating rows in this
// suite (see e.g. tests/reports_test.go).
func seedCompanyActivity(t *testing.T, db *gorm.DB, companyID uint, createdAt time.Time) {
	t.Helper()
	activity := &models.Activity{Type: models.ActivityTypeCall, RelatedType: models.RelatedTypeCompany, RelatedID: companyID}
	require.NoError(t, db.Create(activity).Error)
	require.NoError(t, db.Model(&models.Activity{}).Where("id = ?", activity.ID).Update("created_at", createdAt).Error)
}

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
// widget: always exactly 3 tier groups (even when empty), bucketed by
// last_activity_at (company-scoped Activities only, never contacted counts
// as the most stale tier), and excluding companies contacted within the last
// 60 days or archived companies.
func TestDashboardSummary_UpsellOpportunities(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	neverContacted := seedCompany(t, db)

	tier1Company := seedCompany(t, db)
	seedCompanyActivity(t, db, tier1Company.ID, time.Now().AddDate(0, 0, -70))

	tier2Company := seedCompany(t, db)
	seedCompanyActivity(t, db, tier2Company.ID, time.Now().AddDate(0, 0, -100))

	tier3Company := seedCompany(t, db)
	seedCompanyActivity(t, db, tier3Company.ID, time.Now().AddDate(0, 0, -150))

	recentCompany := seedCompany(t, db)
	seedCompanyActivity(t, db, recentCompany.ID, time.Now().AddDate(0, 0, -5))

	archivedStale := seedCompany(t, db)
	require.NoError(t, db.Model(&models.Company{}).Where("id = ?", archivedStale.ID).Update("status", models.StatusArchived).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			UpsellOpportunities []struct {
				Tier      string `json:"tier"`
				Companies []struct {
					ID   uint   `json:"id"`
					Name string `json:"name"`
				} `json:"companies"`
			} `json:"upsell_opportunities"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data.UpsellOpportunities, 3, "must always report exactly 3 tier groups")

	byTier := map[string][]uint{}
	for _, group := range out.Data.UpsellOpportunities {
		ids := make([]uint, len(group.Companies))
		for i, c := range group.Companies {
			ids[i] = c.ID
		}
		byTier[group.Tier] = ids
	}
	assert.ElementsMatch(t, []string{"tier1", "tier2", "tier3"}, []string{
		out.Data.UpsellOpportunities[0].Tier, out.Data.UpsellOpportunities[1].Tier, out.Data.UpsellOpportunities[2].Tier,
	})

	assert.Contains(t, byTier["tier1"], tier1Company.ID)
	assert.Contains(t, byTier["tier2"], tier2Company.ID)
	assert.Contains(t, byTier["tier3"], tier3Company.ID)
	assert.Contains(t, byTier["tier3"], neverContacted.ID, "never-contacted must land in the most-stale tier")

	for _, ids := range byTier {
		assert.NotContains(t, ids, recentCompany.ID, "recently-contacted company must not appear in any tier")
		assert.NotContains(t, ids, archivedStale.ID, "archived company must not appear in any tier")
	}
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

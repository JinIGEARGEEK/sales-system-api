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

// TestCompanyGet_LastActivityAt guards last_activity_at's definition:
// MAX(activities.created_at) scoped to related_type = 'company' for THIS
// company only — an Activity logged against a Deal (even one belonging to
// this company) must not count, and a Company with no company-scoped
// Activity at all must report null rather than erroring.
func TestCompanyGet_LastActivityAt(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	t.Run("null when no company-scoped Activity exists", func(t *testing.T) {
		company := seedCompany(t, db)

		var out struct {
			Data struct {
				LastActivityAt *time.Time `json:"last_activity_at"`
			} `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies/"+itoa(company.ID), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Nil(t, out.Data.LastActivityAt)
	})

	t.Run("reflects the most recent company-scoped Activity only", func(t *testing.T) {
		company := seedCompany(t, db)
		older := time.Now().AddDate(0, 0, -10)
		newer := time.Now().AddDate(0, 0, -1)
		require.NoError(t, db.Create(&models.Activity{
			Type: models.ActivityTypeCall, RelatedType: models.RelatedTypeCompany, RelatedID: company.ID,
		}).Error)
		require.NoError(t, db.Model(&models.Activity{}).Where("related_id = ? AND related_type = ?", company.ID, models.RelatedTypeCompany).
			Update("created_at", older).Error)
		require.NoError(t, db.Create(&models.Activity{
			Type: models.ActivityTypeEmail, RelatedType: models.RelatedTypeCompany, RelatedID: company.ID,
		}).Error)
		require.NoError(t, db.Model(&models.Activity{}).Where("related_id = ? AND related_type = ? AND type = ?", company.ID, models.RelatedTypeCompany, models.ActivityTypeEmail).
			Update("created_at", newer).Error)

		// Deal-scoped Activity against a Deal that belongs to this company —
		// must NOT count toward last_activity_at (company-scoped only).
		contact := seedContact(t, db, company.ID)
		deal := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: "D", Stage: models.DealStageLead, Status: models.DealStatusOpen}
		require.NoError(t, db.Create(deal).Error)
		veryRecent := time.Now()
		require.NoError(t, db.Create(&models.Activity{
			Type: models.ActivityTypeMeeting, RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID,
		}).Error)
		require.NoError(t, db.Model(&models.Activity{}).Where("related_id = ? AND related_type = ?", deal.ID, models.RelatedTypeDeal).
			Update("created_at", veryRecent).Error)

		var out struct {
			Data struct {
				LastActivityAt *time.Time `json:"last_activity_at"`
			} `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies/"+itoa(company.ID), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotNil(t, out.Data.LastActivityAt)
		assert.WithinDuration(t, newer, *out.Data.LastActivityAt, time.Second, "must reflect the newer company-scoped Activity, not the Deal-scoped one")
	})
}

// TestCompanyList_StaleDaysFilter guards the stale_days query param: only
// companies with no company-scoped Activity within the window (null or
// older than stale_days) come back.
func TestCompanyList_StaleDaysFilter(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	staleCompany := seedCompany(t, db)
	recentCompany := seedCompany(t, db)
	require.NoError(t, db.Create(&models.Activity{
		Type: models.ActivityTypeCall, RelatedType: models.RelatedTypeCompany, RelatedID: recentCompany.ID,
	}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies?stale_days=30", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ids := make([]uint, len(out.Data))
	for i, c := range out.Data {
		ids[i] = c.ID
	}
	assert.Contains(t, ids, staleCompany.ID, "company with no Activity at all must be considered stale")
	assert.NotContains(t, ids, recentCompany.ID, "company with a recent Activity must not be considered stale")
}

// TestCompanyList_HasWonDealFilter guards has_won_deal=true/false.
func TestCompanyList_HasWonDealFilter(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	wonDeal := seedDeal(t, db, nil)
	wonDeal.Status = models.DealStatusWon
	require.NoError(t, db.Save(wonDeal).Error)
	companyWithWonDeal := wonDeal.CompanyID

	companyWithoutWonDeal := seedCompany(t, db)

	t.Run("has_won_deal=true", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies?has_won_deal=true", nil, admin.ID, admin.Role)
		var out struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		ids := make([]uint, len(out.Data))
		for i, c := range out.Data {
			ids[i] = c.ID
		}
		assert.Contains(t, ids, companyWithWonDeal)
		assert.NotContains(t, ids, companyWithoutWonDeal.ID)
	})

	t.Run("has_won_deal=false", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies?has_won_deal=false", nil, admin.ID, admin.Role)
		var out struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		ids := make([]uint, len(out.Data))
		for i, c := range out.Data {
			ids[i] = c.ID
		}
		assert.Contains(t, ids, companyWithoutWonDeal.ID)
		assert.NotContains(t, ids, companyWithWonDeal)
	})
}

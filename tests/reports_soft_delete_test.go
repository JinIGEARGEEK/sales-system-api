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

// Raw Table()/Joins()/EXISTS queries get no GORM soft-delete scoping, so
// these guard each one checking deleted_at itself.

// TestTopReferrers_IgnoresTrashedLeadsAndDeals: a trashed referred Lead
// isn't counted, and a trashed Won Deal's revenue drops out.
func TestTopReferrers_IgnoresTrashedLeadsAndDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	referrer := seedCompany(t, db)
	companyType := models.RelatedTypeCompany

	newLead := func() *models.Lead {
		lead := &models.Lead{Name: "Referred", Source: models.LeadSourceReferral, ReferredByType: &companyType, ReferredByID: &referrer.ID}
		require.NoError(t, db.Create(lead).Error)
		return lead
	}
	kept, trashedLead := newLead(), newLead()
	require.NoError(t, db.Delete(trashedLead).Error)

	liveDeal, trashedDeal := seedDeal(t, db, nil), seedDeal(t, db, nil)
	for _, d := range []*models.Deal{liveDeal, trashedDeal} {
		require.NoError(t, db.Model(d).Updates(map[string]interface{}{"lead_id": kept.ID, "status": models.DealStatusWon, "value": 500}).Error)
	}
	require.NoError(t, db.Delete(trashedDeal).Error)

	var out struct {
		Data []struct {
			LeadsReferred int64   `json:"leads_referred"`
			DealsCreated  int64   `json:"deals_created"`
			DealsWon      int64   `json:"deals_won"`
			WonRevenue    float64 `json:"won_revenue"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/top-referrers", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 1)
	assert.EqualValues(t, 1, out.Data[0].LeadsReferred)
	assert.EqualValues(t, 1, out.Data[0].DealsCreated)
	assert.EqualValues(t, 1, out.Data[0].DealsWon)
	assert.Equal(t, 500.0, out.Data[0].WonRevenue)
}

// TestCustomersByProductStatus_IgnoresTrashedCompanies: a trashed Company's
// CustomerProduct rows aren't listed.
func TestCustomersByProductStatus_IgnoresTrashedCompanies(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	product := &models.Product{Name: "CRM Suite"}
	require.NoError(t, db.Create(product).Error)

	live, trashed := seedCompany(t, db), seedCompany(t, db)
	for _, co := range []*models.Company{live, trashed} {
		require.NoError(t, db.Create(&models.CustomerProduct{CompanyID: co.ID, ProductID: product.ID, StartDate: time.Now()}).Error)
	}
	require.NoError(t, db.Delete(trashed).Error)

	var out struct {
		Data []struct {
			CompanyID uint `json:"company_id"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/customers-by-product-status", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 1)
	assert.Equal(t, live.ID, out.Data[0].CompanyID)
}

// TestCampaignProgress_IgnoresTrashedWonDeals: a Company whose only Won
// Deal is trashed isn't "converted".
func TestCampaignProgress_IgnoresTrashedWonDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	campaign := &models.Campaign{Name: "Win-back", Type: models.CampaignType("win_back")}
	require.NoError(t, db.Create(campaign).Error)

	converted, trashedOnly := seedDeal(t, db, nil), seedDeal(t, db, nil)
	for _, d := range []*models.Deal{converted, trashedOnly} {
		require.NoError(t, db.Model(d).Update("status", models.DealStatusWon).Error)
		require.NoError(t, db.Create(&models.Task{
			RelatedType: models.RelatedTypeCompany, RelatedID: d.CompanyID, Title: "Follow up",
			DueDate: time.Now(), CampaignID: &campaign.ID,
		}).Error)
	}
	require.NoError(t, db.Delete(trashedOnly).Error)

	var out struct {
		Data struct {
			Total     int64 `json:"total"`
			Converted int64 `json:"converted"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/campaigns/"+itoa(campaign.ID)+"/progress", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.EqualValues(t, 2, out.Data.Total)
	assert.EqualValues(t, 1, out.Data.Converted)
}

// TestCompanies_HasWonDealIgnoresTrashedDeals: has_won_deal=true needs a
// live Won Deal.
func TestCompanies_HasWonDealIgnoresTrashedDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(deal).Update("status", models.DealStatusWon).Error)
	require.NoError(t, db.Delete(deal).Error)

	var out struct {
		Total int64 `json:"total"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies?has_won_deal=true", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.EqualValues(t, 0, out.Total)
}

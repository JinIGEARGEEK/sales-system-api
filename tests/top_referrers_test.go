package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestTopReferrers_GroupsByReferrerAcrossCompanyAndContact guards the core
// polymorphic grouping: a Company referrer and a Contact referrer both
// resolve to their own name via the double-LEFT-JOIN, each counted
// separately even though referred_by_id values can collide across the two
// tables (Company id 1 and Contact id 1 are different rows).
func TestTopReferrers_GroupsByReferrerAcrossCompanyAndContact(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	referrerCompany := seedCompany(t, db)
	referrerContact := seedContact(t, db, referrerCompany.ID)

	companyType := models.RelatedTypeCompany
	contactType := models.RelatedTypeContact
	require.NoError(t, db.Create(&models.Lead{
		Name: "Lead A", Source: models.LeadSourceReferral,
		ReferredByType: &companyType, ReferredByID: &referrerCompany.ID,
	}).Error)
	require.NoError(t, db.Create(&models.Lead{
		Name: "Lead B", Source: models.LeadSourceReferral,
		ReferredByType: &contactType, ReferredByID: &referrerContact.ID,
	}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/top-referrers", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			ReferrerType  string  `json:"referrer_type"`
			ReferrerName  string  `json:"referrer_name"`
			LeadsReferred int64   `json:"leads_referred"`
			DealsCreated  int64   `json:"deals_created"`
			DealsWon      int64   `json:"deals_won"`
			WonRevenue    float64 `json:"won_revenue"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 2)

	byType := map[string]int64{}
	for _, r := range out.Data {
		byType[r.ReferrerType] = r.LeadsReferred
		assert.Equal(t, int64(0), r.DealsCreated)
		assert.Equal(t, int64(0), r.DealsWon)
		assert.Equal(t, 0.0, r.WonRevenue)
	}
	assert.Equal(t, int64(1), byType["company"])
	assert.Equal(t, int64(1), byType["contact"])
}

// TestTopReferrers_CountsConvertedAndWonDeals guards the Lead -> Deal join:
// a referred Lead that converted into a Won Deal shows up in deals_created,
// deals_won, and won_revenue.
func TestTopReferrers_CountsConvertedAndWonDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	referrer := seedCompany(t, db)
	companyType := models.RelatedTypeCompany
	lead := &models.Lead{
		Name: "Referred Lead", Source: models.LeadSourceReferral,
		ReferredByType: &companyType, ReferredByID: &referrer.ID,
	}
	require.NoError(t, db.Create(lead).Error)

	dealCompany := seedCompany(t, db)
	contact := seedContact(t, db, dealCompany.ID)
	won := &models.Deal{
		CompanyID: dealCompany.ID, ContactID: contact.ID, Title: "Won Deal", Value: 5000,
		Stage: models.DealStageWon, Status: models.DealStatusWon, LeadID: &lead.ID,
	}
	require.NoError(t, db.Create(won).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/top-referrers", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			LeadsReferred int64   `json:"leads_referred"`
			DealsCreated  int64   `json:"deals_created"`
			DealsWon      int64   `json:"deals_won"`
			WonRevenue    float64 `json:"won_revenue"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 1)
	assert.Equal(t, int64(1), out.Data[0].LeadsReferred)
	assert.Equal(t, int64(1), out.Data[0].DealsCreated)
	assert.Equal(t, int64(1), out.Data[0].DealsWon)
	assert.Equal(t, 5000.0, out.Data[0].WonRevenue)
}

// TestTopReferrers_ExcludesLeadsWithNoReferrer guards the WHERE clause — a
// Lead with no referred_by_id at all must never appear as a row.
func TestTopReferrers_ExcludesLeadsWithNoReferrer(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	seedLead(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/top-referrers", nil, admin.ID, admin.Role)
	var out struct {
		Data []map[string]interface{} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, out.Data)
}

// TestTopReferrers_ForbiddenForSalesRep guards the Admin/Sales-Manager-only
// RBAC, same group as lead-source-conversion.
func TestTopReferrers_ForbiddenForSalesRep(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/top-referrers", nil, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

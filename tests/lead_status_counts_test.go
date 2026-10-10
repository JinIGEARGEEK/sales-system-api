package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestLeadStatusCounts guards GET /leads/status-counts: counts per status
// under GET /leads' filters, with the status filter itself ignored (the
// counts are for every status pill at once).
func TestLeadStatusCounts(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	deal := seedDeal(t, db, nil)

	seed := func(name string, status models.LeadStatus, source models.LeadSource, companyID, dealID *uint) {
		require.NoError(t, db.Create(&models.Lead{
			Name: name, Status: status, Source: source, CompanyID: companyID, ConvertedDealID: dealID,
		}).Error)
	}
	seed("Ann", models.LeadStatusNew, models.LeadSourceWebsite, &company.ID, nil)
	seed("Bea", models.LeadStatusNew, models.LeadSourceReferral, nil, nil)
	seed("Cal", models.LeadStatusContacted, models.LeadSourceWebsite, nil, nil)
	seed("Dee", models.LeadStatusQualified, models.LeadSourceWebsite, nil, &deal.ID)

	counts := func(query string) map[string]int64 {
		var out struct {
			Data map[string]int64 `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads/status-counts"+query, nil, admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)
		return out.Data
	}

	assert.Equal(t, map[string]int64{"New": 2, "Contacted": 1}, counts("?exclude_converted=true&status=Contacted"))
	assert.Equal(t, map[string]int64{"New": 1, "Contacted": 1, "Qualified": 1}, counts("?source=Website"))
	// search LEFT JOINs companies, which has its own status column.
	assert.Equal(t, map[string]int64{"New": 1}, counts("?search=acme"))

	t.Run("Production is refused", func(t *testing.T) {
		production := testutil.CreateUser(t, db, models.RoleProduction)
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads/status-counts", nil, production.ID, production.Role)
		assert.Equal(t, http.StatusForbidden, doJSON(t, app, req, nil).StatusCode)
	})
}

package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestQuoteUpdate_DoesNotClobberValidityDate guards against the "partial-update
// clobbering" regression: PUT /quotes/:id with a body that omits validity_date
// must leave the stored value untouched, not null it out.
func TestQuoteUpdate_DoesNotClobberValidityDate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	validity := "2027-01-01"
	quote := &models.Quote{DealID: deal.ID, Items: models.JSONItems{}, ValidityDate: &validity, Status: models.QuoteStatusDraft}
	require.NoError(t, db.Create(quote).Error)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(quote.ID), map[string]interface{}{
		"status": "sent",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Quote
	require.NoError(t, db.First(&reloaded, quote.ID).Error)
	assert.Equal(t, models.QuoteStatusSent, reloaded.Status)
	require.NotNil(t, reloaded.ValidityDate, "validity_date must not be nulled out by an update that omits it")
	assert.Equal(t, validity, *reloaded.ValidityDate)
}

// TestProjectUpdate_DoesNotClobberDealID guards against the same class of bug
// on Project.DealID via PATCH /projects/:id.
func TestProjectUpdate_DoesNotClobberDealID(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	company := seedCompany(t, db)

	project := &models.Project{CompanyID: company.ID, DealID: &deal.ID, Name: "Website Revamp", Status: models.ProjectStatusNotStarted}
	require.NoError(t, db.Create(project).Error)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/projects/"+itoa(project.ID), map[string]interface{}{
		"status": "In Progress",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Project
	require.NoError(t, db.First(&reloaded, project.ID).Error)
	assert.Equal(t, models.ProjectStatusInProgress, reloaded.Status)
	require.NotNil(t, reloaded.DealID, "deal_id must not be nulled out by an update that omits it")
	assert.Equal(t, deal.ID, *reloaded.DealID)
}

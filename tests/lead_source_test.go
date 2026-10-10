package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestLeadUpdate_KeepsUnchangedInactiveSource guards that a Lead carrying a
// source outside the active Lead source list (a Prospect-only source like
// "Marketing Campaign", copied over by POST /prospects/:id/convert) can
// still be saved while its source is left unchanged — but not switched to
// another invalid one.
func TestLeadUpdate_KeepsUnchangedInactiveSource(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	lead := seedLead(t, db, nil)
	require.NoError(t, db.Model(lead).Update("source", "Marketing Campaign").Error)

	put := func(source string) int {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
			"name": lead.Name, "source": source, "status": "Contacted",
		}, admin.ID, admin.Role)
		return doJSON(t, app, req, nil).StatusCode
	}
	assert.Equal(t, http.StatusOK, put("Marketing Campaign"))
	assert.Equal(t, http.StatusUnprocessableEntity, put("LINE OA"))
}

// TestDealUpdate_KeepsUnchangedInactiveChannel is the Deal side of the same
// rule: a Lead's source becomes its Deal's channel on conversion.
func TestDealUpdate_KeepsUnchangedInactiveChannel(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(deal).Update("channel", "Marketing Campaign").Error)

	put := func(channel string) int {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), map[string]interface{}{
			"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": deal.Title,
			"value": deal.Value, "stage": string(deal.Stage), "status": string(deal.Status), "channel": channel,
		}, admin.ID, admin.Role)
		return doJSON(t, app, req, nil).StatusCode
	}
	assert.Equal(t, http.StatusOK, put("Marketing Campaign"))
	assert.Equal(t, http.StatusUnprocessableEntity, put("LINE OA"))
}

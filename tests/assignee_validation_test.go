package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// dealPutBody is a full PUT /deals/:id body for deal with assigned_to set.
func dealPutBody(deal *models.Deal, assignedTo *uint) map[string]interface{} {
	return map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": deal.Title,
		"value": deal.Value, "stage": deal.Stage, "assigned_to": assignedTo,
	}
}

// badAssignees returns a Production user and an inactive Sales Rep — the
// two kinds of user validateAssignee rejects — plus a nonexistent id.
func badAssignees(t *testing.T, db *gorm.DB) map[string]uint {
	t.Helper()
	production := testutil.CreateUser(t, db, models.RoleProduction)
	inactive := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(inactive).Update("is_active", false).Error)
	return map[string]uint{"production": production.ID, "inactive": inactive.ID, "missing": 99999}
}

func assertAssigneeRejected(t *testing.T, resp *http.Response, body sessionErrBody, name string) {
	t.Helper()
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, name)
	assert.True(t, errField(body, "assigned_to"), name)
}

// TestDealAssignee_CreateUpdateReassignRejectInvalid — deal Create, Update
// and Reassign only accept an active user in a sales-pipeline role.
func TestDealAssignee_CreateUpdateReassignRejectInvalid(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	for name, id := range badAssignees(t, db) {
		id := id
		var body sessionErrBody
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
			"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "New", "assigned_to": id,
		}, admin.ID, admin.Role)
		assertAssigneeRejected(t, doJSON(t, app, req, &body), body, "create "+name)

		body = sessionErrBody{}
		req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), dealPutBody(deal, &id), admin.ID, admin.Role)
		assertAssigneeRejected(t, doJSON(t, app, req, &body), body, "update "+name)

		body = sessionErrBody{}
		req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/reassign", map[string]interface{}{"assigned_to": id}, admin.ID, admin.Role)
		assertAssigneeRejected(t, doJSON(t, app, req, &body), body, "reassign "+name)
	}

	var stored models.Deal
	require.NoError(t, db.First(&stored, deal.ID).Error)
	assert.Nil(t, stored.AssignedTo)

	// Every sales-pipeline role is a valid owner, and null still unassigns.
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "New", "assigned_to": marketing.ID,
	}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusCreated, doJSON(t, app, req, nil).StatusCode)
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/reassign", map[string]interface{}{"assigned_to": nil}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
}

// TestDealUpdate_UnchangedStaleAssigneeStillEditable — the assignee is only
// re-validated when it changes, so a deal whose owner was deactivated can
// still be edited (e.g. renamed) before it's reassigned.
func TestDealUpdate_UnchangedStaleAssigneeStillEditable(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	gone := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, &gone.ID)
	require.NoError(t, db.Model(gone).Update("is_active", false).Error)

	body := dealPutBody(deal, &gone.ID)
	body["title"] = "Renamed"
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
}

// TestDealUpdate_SalesRepCannotUnassignOrHandOff — on PUT a Sales Rep may
// keep their deal or claim an unassigned one, but not unassign it or give
// it to someone else (403).
func TestDealUpdate_SalesRepCannotUnassignOrHandOff(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	other := testutil.CreateUser(t, db, models.RoleSalesRep)
	own := seedDeal(t, db, &rep.ID)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(own.ID), dealPutBody(own, nil), rep.ID, rep.Role)
	assert.Equal(t, http.StatusForbidden, doJSON(t, app, req, nil).StatusCode, "unassign")
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(own.ID), dealPutBody(own, &other.ID), rep.ID, rep.Role)
	assert.Equal(t, http.StatusForbidden, doJSON(t, app, req, nil).StatusCode, "hand off")

	var stored models.Deal
	require.NoError(t, db.First(&stored, own.ID).Error)
	require.NotNil(t, stored.AssignedTo)
	assert.Equal(t, rep.ID, *stored.AssignedTo)

	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(own.ID), dealPutBody(own, &rep.ID), rep.ID, rep.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode, "keep")

	unassigned := seedDeal(t, db, nil)
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(unassigned.ID), dealPutBody(unassigned, nil), rep.ID, rep.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode, "edit unassigned, leaving it unassigned")
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(unassigned.ID), dealPutBody(unassigned, &rep.ID), rep.ID, rep.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode, "claim")

	// A manager may still unassign.
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(own.ID), dealPutBody(own, nil), manager.ID, manager.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode, "manager unassign")
}

// TestDealUpdate_AuditsValueCompanyAndOwner — PUT writes an "updated" row for
// value/company_id changes and a "reassigned" row for an owner change, with
// before/after, and nothing extra when they're unchanged.
func TestDealUpdate_AuditsValueCompanyAndOwner(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, nil)
	newCompany := &models.Company{Name: "Other Corp", Status: models.StatusActive}
	require.NoError(t, db.Create(newCompany).Error)

	body := dealPutBody(deal, &rep.ID)
	body["value"] = 2500.0
	body["company_id"] = newCompany.ID
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)

	var updated, reassigned models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "deal", deal.ID, "updated").Take(&updated).Error)
	assert.EqualValues(t, 1000, updated.Before["value"])
	assert.EqualValues(t, 2500, updated.After["value"])
	assert.EqualValues(t, deal.CompanyID, updated.Before["company_id"])
	assert.EqualValues(t, newCompany.ID, updated.After["company_id"])
	assert.Equal(t, admin.ID, updated.ActorID)

	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "deal", deal.ID, "reassigned").Take(&reassigned).Error)
	assert.Nil(t, reassigned.Before["assigned_to"])
	assert.EqualValues(t, rep.ID, reassigned.After["assigned_to"])

	// Resubmitting the same values adds no rows.
	require.NoError(t, db.First(deal, deal.ID).Error)
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), dealPutBody(deal, &rep.ID), admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
	assert.Equal(t, []string{"updated", "reassigned"}, auditActions(t, db, "deal", deal.ID))
}

// TestBulkReassign_RejectsInvalidAssignee — the shared bulk reassign used by
// deals, leads and prospects validates the new assignee up front and
// changes nothing on failure.
func TestBulkReassign_RejectsInvalidAssignee(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	lead := models.Lead{Name: "Lead", Status: models.LeadStatusNew}
	require.NoError(t, db.Create(&lead).Error)
	prospect := seedProspect(t, db, nil)

	for name, id := range badAssignees(t, db) {
		for path, rowID := range map[string]uint{"deals": deal.ID, "leads": lead.ID, "prospects": prospect.ID} {
			var body sessionErrBody
			req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/"+path+"/bulk-reassign",
				map[string]interface{}{"ids": []uint{rowID}, "assigned_to": id}, admin.ID, admin.Role)
			assertAssigneeRejected(t, doJSON(t, app, req, &body), body, path+" "+name)
		}
	}
	assert.Nil(t, assigneeOf(t, db, &models.Deal{}, deal.ID))
	assert.Nil(t, assigneeOf(t, db, &models.Lead{}, lead.ID))
	assert.Nil(t, assigneeOf(t, db, &models.Prospect{}, prospect.ID))

	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/bulk-reassign",
		map[string]interface{}{"ids": []uint{deal.ID}, "assigned_to": rep.ID}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusNoContent, doJSON(t, app, req, nil).StatusCode)
}

package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDealUpdate_RejectsMissingCompanyID guards against the "Deal FK zeroing"
// regression: PUT /deals/:id with a body missing company_id must 422 and must
// NOT have zeroed out the deal's company_id in the DB.
func TestDealUpdate_RejectsMissingCompanyID(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	originalCompanyID := deal.CompanyID

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), map[string]interface{}{
		"contact_id": deal.ContactID,
		"title":      "Missing company_id on purpose",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, originalCompanyID, reloaded.CompanyID, "company_id must not be zeroed before the validation check ran")
}

// TestDealReassign_WritesAuditLog guards against a missing audit trail on
// PATCH /deals/:id/reassign: an Admin reassigning a deal must produce an
// audit_log_entries row with entity_type=deal, action=reassigned.
func TestDealReassign_WritesAuditLog(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	newOwner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/reassign", map[string]interface{}{
		"assigned_to": newOwner.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "deal", deal.ID, "reassigned").Find(&entries).Error)
	require.Len(t, entries, 1, "expected exactly one reassign audit log entry")
	assert.Equal(t, admin.ID, entries[0].ActorID)
}

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

// TestNotificationLogList_CompanyBranch guards GET /notification-log's
// "company" entity-type branch: it resolves the Company, scopes visibility
// via CanWrite against the company's most-recent Deal's owner (same
// resolution checkCompanyDormantRule uses), and returns company_id/
// company_name additively alongside the existing deal/prospect fields.
func TestNotificationLogList_CompanyBranch(t *testing.T) {
	app, db := testutil.App(t)
	repA := testutil.CreateUser(t, db, models.RoleSalesRep)
	repB := testutil.CreateUser(t, db, models.RoleSalesRep)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	rule := &models.NotificationRule{
		Name: "Dormant company test rule", EntityType: models.NotificationEntityCompany,
		ThresholdDays: 60, RecipientRole: models.NotificationRecipientOwner, IsActive: true,
	}
	require.NoError(t, db.Create(rule).Error)

	// Company owned (via most-recent Deal's AssignedTo) by repA.
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	deal := &models.Deal{
		CompanyID: company.ID, ContactID: contact.ID, Title: "Owned Deal",
		Stage: models.DealStageLead, Status: models.DealStatusOpen, AssignedTo: &repA.ID,
	}
	require.NoError(t, db.Create(deal).Error)

	log := &models.NotificationLog{RuleID: rule.ID, EntityID: company.ID, Context: "60", NotifiedAt: time.Now()}
	require.NoError(t, db.Create(log).Error)

	t.Run("visible to the resolved owner", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/notification-log", nil, repA.ID, repA.Role)
		var out struct {
			Data []struct {
				EntityType  string `json:"entity_type"`
				CompanyID   uint   `json:"company_id"`
				CompanyName string `json:"company_name"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 1)
		assert.Equal(t, "company", out.Data[0].EntityType)
		assert.Equal(t, company.ID, out.Data[0].CompanyID)
		assert.Equal(t, company.Name, out.Data[0].CompanyName)
	})

	t.Run("hidden from an unrelated Sales Rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/notification-log", nil, repB.ID, repB.Role)
		var out struct {
			Data []struct {
				CompanyID uint `json:"company_id"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		for _, row := range out.Data {
			assert.NotEqual(t, company.ID, row.CompanyID)
		}
	})

	t.Run("visible to admin regardless of owner", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/notification-log", nil, admin.ID, admin.Role)
		var out struct {
			Data []struct {
				CompanyID uint `json:"company_id"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		found := false
		for _, row := range out.Data {
			if row.CompanyID == company.ID {
				found = true
			}
		}
		assert.True(t, found)
	})
}

// TestNotificationLogList_CompanyBranch_NoDeal guards the no-Deal case:
// ownerID resolves to nil, which CanWrite treats identically to an
// unassigned Deal elsewhere in this file (visible, not a 500).
func TestNotificationLogList_CompanyBranch_NoDeal(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	rule := &models.NotificationRule{
		Name: "Dormant company test rule (no deal)", EntityType: models.NotificationEntityCompany,
		ThresholdDays: 60, RecipientRole: models.NotificationRecipientOwner, IsActive: true,
	}
	require.NoError(t, db.Create(rule).Error)

	company := seedCompany(t, db)
	log := &models.NotificationLog{RuleID: rule.ID, EntityID: company.ID, Context: "120", NotifiedAt: time.Now()}
	require.NoError(t, db.Create(log).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/notification-log", nil, rep.ID, rep.Role)
	var out struct {
		Data []struct {
			CompanyID uint `json:"company_id"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	found := false
	for _, row := range out.Data {
		if row.CompanyID == company.ID {
			found = true
		}
	}
	assert.True(t, found, "a company with no Deal at all must still be visible, not hidden or a 500")
}

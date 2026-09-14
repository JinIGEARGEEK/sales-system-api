package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestNotificationRule_CreateUpdateDelete guards the Admin CRUD for the
// configurable workflow notification rule list (FR-CRM-100/101/102) —
// List/Create/Update/Delete, Delete being a soft "is_active: false" flip.
func TestNotificationRule_CreateUpdateDelete(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/notification-rules", map[string]interface{}{
		"name": "Quote expiring soon", "entity_type": "quote", "threshold_days": 3, "recipient_role": "owner",
	}, admin.ID, admin.Role)
	var created struct {
		Data models.NotificationRule `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, models.NotificationEntityQuote, created.Data.EntityType)
	require.True(t, created.Data.IsActive)

	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/notification-rules", nil, admin.ID, admin.Role)
	var list struct {
		Data []models.NotificationRule `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.Len(t, list.Data, 1)

	updateReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/notification-rules/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Quote expiring soon (5d)", "entity_type": "quote", "threshold_days": 5, "recipient_role": "owner_and_managers",
	}, admin.ID, admin.Role)
	var updated struct {
		Data models.NotificationRule `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, 5, updated.Data.ThresholdDays)
	require.Equal(t, models.NotificationRecipientOwnerAndManagers, updated.Data.RecipientRole)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/admin/notification-rules/"+itoa(created.Data.ID), nil, admin.ID, admin.Role)
	deleteResp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, fiber.StatusNoContent, deleteResp.StatusCode)

	var afterDelete models.NotificationRule
	require.NoError(t, db.First(&afterDelete, created.Data.ID).Error)
	require.False(t, afterDelete.IsActive, "Delete deactivates rather than removing the row")
}

// TestNotificationRule_CreateRejectsInvalidFields guards each of
// validateNotificationRuleForm's checks: invalid entity_type/recipient_role,
// and threshold_days <= 0.
func TestNotificationRule_CreateRejectsInvalidFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	cases := []map[string]interface{}{
		{"name": "x", "entity_type": "not-a-type", "threshold_days": 3, "recipient_role": "owner"},
		{"name": "x", "entity_type": "quote", "threshold_days": 3, "recipient_role": "not-a-role"},
		{"name": "x", "entity_type": "quote", "threshold_days": 0, "recipient_role": "owner"},
	}
	for _, body := range cases {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/notification-rules", body, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
	}
}

// TestNotificationRule_ListForbiddenForNonAdmin guards the Admin-only route
// gate.
func TestNotificationRule_ListForbiddenForNonAdmin(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/notification-rules", nil, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

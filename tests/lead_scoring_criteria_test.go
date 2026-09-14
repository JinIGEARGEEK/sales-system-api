package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestLeadScoringCriteria_CreateUpdateDelete guards the Admin CRUD for the
// configurable lead-scoring rule list (FR-CRM-006) — List/Create/Update/
// Delete, Delete being a soft "is_active: false" flip (not a row delete).
func TestLeadScoringCriteria_CreateUpdateDelete(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/lead-scoring-criteria", map[string]interface{}{
		"name": "Has phone", "field": "has_phone", "weight": 10,
	}, admin.ID, admin.Role)
	var created struct {
		Data models.LeadScoringCriterion `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Has phone", created.Data.Name)
	require.True(t, created.Data.IsActive)

	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/lead-scoring-criteria", nil, admin.ID, admin.Role)
	var list struct {
		Data []models.LeadScoringCriterion `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.Len(t, list.Data, 1)

	updateReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/lead-scoring-criteria/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Has phone number", "field": "has_phone", "weight": 15,
	}, admin.ID, admin.Role)
	var updated struct {
		Data models.LeadScoringCriterion `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, 15, updated.Data.Weight)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/admin/lead-scoring-criteria/"+itoa(created.Data.ID), nil, admin.ID, admin.Role)
	deleteResp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, fiber.StatusNoContent, deleteResp.StatusCode)

	var afterDelete models.LeadScoringCriterion
	require.NoError(t, db.First(&afterDelete, created.Data.ID).Error)
	require.False(t, afterDelete.IsActive, "Delete deactivates rather than removing the row")
}

// TestLeadScoringCriteria_CreateRequiresNameAndField guards the required-
// field validation on Create.
func TestLeadScoringCriteria_CreateRequiresNameAndField(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	missingName := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/lead-scoring-criteria", map[string]interface{}{
		"field": "has_phone",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, missingName, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)

	missingField := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/lead-scoring-criteria", map[string]interface{}{
		"name": "Has phone",
	}, admin.ID, admin.Role)
	fieldResp := doJSON(t, app, missingField, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, fieldResp.StatusCode)
}

// TestLeadScoringCriteria_DuplicateNameRejected guards the unique-name
// constraint's 422 mapping.
func TestLeadScoringCriteria_DuplicateNameRejected(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	body := map[string]interface{}{"name": "Has phone", "field": "has_phone", "weight": 10}
	first := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/lead-scoring-criteria", body, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusCreated, doJSON(t, app, first, nil).StatusCode)

	dup := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/lead-scoring-criteria", body, admin.ID, admin.Role)
	dupResp := doJSON(t, app, dup, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, dupResp.StatusCode)
}

// TestLeadScoringCriteria_ListForbiddenForNonAdmin guards the Admin-only
// route gate.
func TestLeadScoringCriteria_ListForbiddenForNonAdmin(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/lead-scoring-criteria", nil, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

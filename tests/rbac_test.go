package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestRBAC_RouteGates checks the route-level RequireRoles gates: Admin-only
// /users and Admin/Sales-Manager-only /reports/* must 403 a Sales Rep and
// 200 an Admin.
func TestRBAC_RouteGates(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)

	routes := []string{
		"/api/v1/users",
		"/api/v1/reports/lead-source-conversion",
		// /api/v1/audit-log was here (Admin-only) until 2026-09-08, when the
		// route was opened to Sales Rep/Sales Manager too so Deal
		// stage-change history could surface in the Activities pages as
		// read-only context — see TestRBAC_AuditLogRestrictedForNonAdmin
		// below for its own regression guard (both the route-gate — Admin/
		// Sales Rep/Sales Manager 200, everyone else 403 — and the
		// handler-level restriction of what a non-Admin can actually see).
	}

	for _, path := range routes {
		t.Run(path+"_rep_forbidden", func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodGet, path, nil, rep.ID, rep.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})

		t.Run(path+"_admin_ok", func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodGet, path, nil, admin.ID, admin.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	// Reports also allow Sales Manager (unlike /users, which is Admin-only).
	t.Run("reports_manager_ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/lead-source-conversion", nil, manager.ID, manager.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("users_manager_forbidden", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/users", nil, manager.ID, manager.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "/users is Admin-only, not Sales-Manager")
	})
}

// TestRBAC_ProspectsAllowMarketingSalesManagerAndSalesRep covers the roles
// the shared loop above doesn't: /prospects is Admin/Marketing/Sales-Manager/
// Sales-Rep (unlike /users' Admin-only or /reports' Admin/Sales-Manager), so
// Marketing (its primary owner), Sales Manager (oversight), and Sales Rep
// (works Prospects ahead of the Lead hand-off) must all get through, not
// just Admin.
func TestRBAC_ProspectsAllowMarketingSalesManagerAndSalesRep(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	t.Run("marketing_ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/prospects", nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("sales_manager_ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/prospects", nil, manager.ID, manager.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("sales_rep_ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/prospects", nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// TestRBAC_TagsWritesAreRestricted guards a gap where the /tags group had no
// role restriction at all (unlike the structurally identical
// PipelineStage/LeadSource config, both Admin-only): any authenticated role,
// including Sales Rep, could rename/deactivate/create shared tags used across
// Companies/Deals/Contacts. List stays open to every role (tag pickers need
// it); writes are now Admin/Sales-Manager only, same as bulkRoles elsewhere.
func TestRBAC_TagsWritesAreRestricted(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	t.Run("list is open to a sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/tags", nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("create is forbidden for a sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/tags", map[string]interface{}{
			"name": "Enterprise", "category": "Tier",
		}, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("create succeeds for an admin", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/tags", map[string]interface{}{
			"name": "Enterprise", "category": "Tier",
		}, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	})
}

// TestRBAC_AuditLogRestrictedForNonAdmin covers /audit-log's 2026-09-08
// change: the route itself opened up from Admin-only to Admin/Sales Rep/
// Sales Manager (so Deal stage-change history can surface in the Activities
// pages as read-only context), but List() then hard-restricts what a
// non-Admin caller actually gets back — entity_type=deal, action=stage_changed
// only, regardless of what they ask for — so this needs its own regression
// guard beyond the plain route-gate check in TestRBAC_RouteGates above.
func TestRBAC_AuditLogRestrictedForNonAdmin(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	deal := seedDeal(t, db, nil)

	// Three different audit rows on/around the same Deal — only the first
	// should ever reach a non-Admin caller, regardless of the query they send.
	stageChange := models.AuditLogEntry{
		EntityType: "deal", EntityID: deal.ID, Action: "stage_changed",
		Before: models.JSONMap{"stage": "Lead"}, After: models.JSONMap{"stage": "Qualified"},
		ActorID: admin.ID,
	}
	reassigned := models.AuditLogEntry{
		EntityType: "deal", EntityID: deal.ID, Action: "reassigned",
		Before: models.JSONMap{"assigned_to": nil}, After: models.JSONMap{"assigned_to": rep.ID},
		ActorID: admin.ID,
	}
	settingsChange := models.AuditLogEntry{
		EntityType: "settings", EntityID: 1, Action: "updated",
		Before: models.JSONMap{"lead_scoring_mql_threshold": 0}, After: models.JSONMap{"lead_scoring_mql_threshold": 50},
		ActorID: admin.ID,
	}
	require.NoError(t, db.Create(&stageChange).Error)
	require.NoError(t, db.Create(&reassigned).Error)
	require.NoError(t, db.Create(&settingsChange).Error)

	type listResponse struct {
		Data []models.AuditLogEntry `json:"data"`
	}

	t.Run("production is still forbidden by the route gate", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log", nil, production.ID, production.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("sales rep with no filters sees only the stage-change entry", func(t *testing.T) {
		var out listResponse
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log", nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 1)
		assert.Equal(t, "stage_changed", out.Data[0].Action)
		assert.Equal(t, stageChange.ID, out.Data[0].ID)
	})

	t.Run("sales manager asking for settings/reassigned still only gets the stage-change entry", func(t *testing.T) {
		var out listResponse
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log?entity_type=settings&actor_id="+itoa(admin.ID), nil, manager.ID, manager.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 1, "entity_type/actor_id from a non-Admin caller must be ignored, not honored")
		assert.Equal(t, "stage_changed", out.Data[0].Action)
	})

	t.Run("admin sees all three entries", func(t *testing.T) {
		var out listResponse
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Len(t, out.Data, 3)
	})
}

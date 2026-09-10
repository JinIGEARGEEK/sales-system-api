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
// Sales Manager (so Deal history can surface in the Activities/Deal-detail
// pages as read-only context), but List() then hard-restricts what a
// non-Admin caller actually gets back to entity_type=deal, regardless of what
// entity_type/actor_id they ask for — with a further split by role: Sales Rep
// gets stage_changed only, Sales Manager also gets reassigned/bulk_reassigned
// (the Owner History card, FR-CRM-025/M-8) — so this needs its own
// regression guard beyond the plain route-gate check in TestRBAC_RouteGates
// above.
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

	t.Run("sales manager asking for settings still gets both deal entries, never the settings one", func(t *testing.T) {
		var out listResponse
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log?entity_type=settings&actor_id="+itoa(admin.ID), nil, manager.ID, manager.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 2, "entity_type/actor_id from a non-Admin caller must be ignored, not honored")
		actions := []string{out.Data[0].Action, out.Data[1].Action}
		assert.ElementsMatch(t, []string{"stage_changed", "reassigned"}, actions)
	})

	t.Run("admin sees all three entries", func(t *testing.T) {
		var out listResponse
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Len(t, out.Data, 3)
	})
}

// TestRBAC_PipelineStagesLeadSourcesListOpenWritesAdminOnly guards a
// 2026-09-09 fix: GET /admin/pipeline-stages, GET /admin/lead-sources, GET
// /admin/product-categories, GET /admin/industries, GET
// /admin/company-sizes, GET /admin/revenue-sizes, and GET /admin/job-titles
// were each inside the same Admin-only route group as their own
// Create/Update/Delete, so every non-Admin role got a silent 403 loading any
// of them, even though every role's own Deal/Lead/Contact/Company create/edit
// forms, the shared Dashboard/Kanban board, and pages/crm/projects/index.vue's
// Products tab need these for their stage/source/category/industry/size/
// job-title dropdowns — companies/contacts/projects aren't Admin-gated
// pages, so this broke them for Sales Rep/Sales Manager/Marketing/Production
// alike, not just Production. List is now registered directly on `authed`
// (open to any authenticated role) for all seven; the writes stay behind the
// Admin-only group, same as TestRBAC_TagsWritesAreRestricted's list/write
// split for /tags.
func TestRBAC_PipelineStagesLeadSourcesListOpenWritesAdminOnly(t *testing.T) {
	app, db := testutil.App(t)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	for _, path := range []string{
		"/api/v1/admin/pipeline-stages", "/api/v1/admin/lead-sources", "/api/v1/admin/product-categories",
		"/api/v1/admin/industries", "/api/v1/admin/company-sizes", "/api/v1/admin/revenue-sizes", "/api/v1/admin/job-titles",
	} {
		t.Run(path+"_list_open_to_production", func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodGet, path, nil, production.ID, production.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run(path+"_create_forbidden_for_production", func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPost, path, map[string]interface{}{"name": "x"}, production.ID, production.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})

		t.Run(path+"_list_open_to_admin", func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodGet, path, nil, admin.ID, admin.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

// TestRBAC_LeadUpdateConvertSalesPipelineOnly guards a 2026-09-09 fix: PUT
// /leads/:id and POST /leads/:id/convert had no role check at all — Marketing
// has no nav access to /crm/leads (deliberately: Marketing's own scope is
// Prospects only, FR-CRM-105/106) but the frontend's Mark SQL/Convert to Deal
// buttons on the Lead detail page were only ever hidden by convention, not
// actually blocked server-side, so a Marketing (or Production) caller hitting
// either endpoint directly would have succeeded. GET /leads/:id stays open —
// that's the read-only "View Lead" access this fix is meant to preserve, via
// the Prospect detail page's link to a converted Lead.
func TestRBAC_LeadUpdateConvertSalesPipelineOnly(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	lead := seedLead(t, db, nil)

	t.Run("get is still open to marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads/"+itoa(lead.ID), nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("update is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
			"name": lead.Name, "source": string(lead.Source), "status": "Qualified",
		}, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("convert is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
			"deal": map[string]interface{}{"title": "x", "value": 100, "stage": "Lead"},
		}, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("update is allowed for sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
			"name": lead.Name, "source": string(lead.Source), "status": "Qualified",
		}, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// TestRBAC_LeadListCreateDeleteSalesPipelineOnly guards the other three Lead
// mutations that had no role check at all until this fix — the same gap
// Update/Convert had until 2026-09-09, just never carried over to List/
// Create/Delete. GET by id stays open (Marketing's "View Lead" read access).
func TestRBAC_LeadListCreateDeleteSalesPipelineOnly(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	t.Run("list is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads", nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("create is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
			"name": "Test Lead", "source": "Website",
		}, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	lead := seedLead(t, db, nil)
	t.Run("delete is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/leads/"+itoa(lead.ID), nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("list/create/delete are allowed for sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads", nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		req = testutil.AuthRequest(t, http.MethodDelete, "/api/v1/leads/"+itoa(lead.ID), nil, rep.ID, rep.Role)
		resp = doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

// TestRBAC_DealsSalesPipelineOnly guards the Deals resource group (list,
// create, get, update, delete, stage-move, and the nested Quote/Payment/
// Contract sub-resources) — spec §1.7 states Marketing/Production have "no
// access to Leads/Deals/any other resource", but every one of these routes
// was previously open to any authenticated role.
func TestRBAC_DealsSalesPipelineOnly(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, nil)

	t.Run("list is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals", nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("get by id is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID), nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("quotes sub-resource is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("list and get are allowed for sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals", nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		req = testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID), nil, rep.ID, rep.Role)
		resp = doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// TestRBAC_ProductCatalogWritesAdminOnly guards spec §8.2: "Product Catalog
// CRUD (Admin only)". Create/Update/Deactivate were previously open to any
// authenticated role; List stays open (Deal/Quote line-item forms need it
// regardless of role).
func TestRBAC_ProductCatalogWritesAdminOnly(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	t.Run("list is open to sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/products", nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("create is forbidden for sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/products", map[string]interface{}{
			"name": "Test Product", "category": "",
		}, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("create is allowed for admin, then update/deactivate are forbidden for sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/products", map[string]interface{}{
			"name": "Test Product", "category": "",
		}, admin.ID, admin.Role)
		var out struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		require.NotZero(t, out.Data.ID)

		req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/products/"+itoa(out.Data.ID), map[string]interface{}{
			"name": "Renamed",
		}, rep.ID, rep.Role)
		resp = doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)

		req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/products/"+itoa(out.Data.ID)+"/deactivate", nil, rep.ID, rep.Role)
		resp = doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

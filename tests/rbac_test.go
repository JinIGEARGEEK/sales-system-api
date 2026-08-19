package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

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
		// Admin-only, append-only per NFR-007 — regression guard for a bug
		// where middleware.RequireRoles was registered AFTER the handler in
		// routes.go (auditLogH.List, adminOnly instead of adminOnly,
		// auditLogH.List), so it never actually ran: the handler doesn't call
		// c.Next(), so any authenticated role could read the full audit trail.
		"/api/v1/audit-log",
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

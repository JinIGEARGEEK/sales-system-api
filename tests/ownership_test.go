package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDealOwnership_SubResourceBypass guards against the "ownership bypass on
// Deal sub-resources" regression: a Sales Rep blocked from writing another
// rep's Deal must also be blocked from creating Quotes/Payments/Contracts on
// that Deal, and must succeed once the ownership check passes.
func TestDealOwnership_SubResourceBypass(t *testing.T) {
	app, db := testutil.App(t)

	repA := testutil.CreateUser(t, db, models.RoleSalesRep)
	repB := testutil.CreateUser(t, db, models.RoleSalesRep)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, &repA.ID)

	type call struct {
		name   string
		method string
		path   string
		body   interface{}
	}
	fullPath := func(cCase call) string {
		path := "/api/v1/deals/" + itoa(deal.ID)
		if cCase.path != "" {
			path += cCase.path
		}
		return path
	}
	calls := []call{
		{"PUT deal", http.MethodPut, "", map[string]interface{}{
			"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Updated Title",
			"assigned_to": repA.ID,
		}},
		{"POST quote", http.MethodPost, "/quotes", map[string]interface{}{"status": "draft"}},
		{"POST payment", http.MethodPost, "/payments", map[string]interface{}{"amount": 100}},
		{"POST contract", http.MethodPost, "/contracts", map[string]interface{}{"status": "draft"}},
	}

	for _, cCase := range calls {
		path := fullPath(cCase)

		t.Run(cCase.name+"_forbidden_for_other_rep", func(t *testing.T) {
			req := testutil.AuthRequest(t, cCase.method, path, cCase.body, repB.ID, repB.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode, "rep B must not write repA's deal or its sub-resources")
		})
	}

	for _, cCase := range calls {
		path := fullPath(cCase)

		t.Run(cCase.name+"_allowed_for_owner", func(t *testing.T) {
			req := testutil.AuthRequest(t, cCase.method, path, cCase.body, repA.ID, repA.Role)
			resp := doJSON(t, app, req, nil)
			assert.True(t, resp.StatusCode < 300, "owning rep should succeed, got %d", resp.StatusCode)
		})

		t.Run(cCase.name+"_allowed_for_admin", func(t *testing.T) {
			req := testutil.AuthRequest(t, cCase.method, path, cCase.body, admin.ID, admin.Role)
			resp := doJSON(t, app, req, nil)
			assert.True(t, resp.StatusCode < 300, "admin should succeed, got %d", resp.StatusCode)
		})
	}
}

// TestActivityDelete_Ownership guards against the "Activity delete ownership"
// regression: DELETE /activities/:id must 403 for a Sales Rep who isn't the
// creator, and succeed for the creator or a manager/admin.
func TestActivityDelete_Ownership(t *testing.T) {
	app, db := testutil.App(t)

	repA := testutil.CreateUser(t, db, models.RoleSalesRep)
	repB := testutil.CreateUser(t, db, models.RoleSalesRep)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	createActivity := func() *models.Activity {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/activities", map[string]interface{}{
			"type": "call", "subject": "call", "related_type": "company", "related_id": company.ID,
		}, repA.ID, repA.Role)
		var body struct {
			Data models.Activity `json:"data"`
		}
		resp := doJSON(t, app, req, &body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("failed to seed activity, status=%d", resp.StatusCode)
		}
		return &body.Data
	}

	t.Run("forbidden for non-creator rep", func(t *testing.T) {
		activity := createActivity()
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/activities/"+itoa(activity.ID), nil, repB.ID, repB.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("allowed for creator", func(t *testing.T) {
		activity := createActivity()
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/activities/"+itoa(activity.ID), nil, repA.ID, repA.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("allowed for admin", func(t *testing.T) {
		activity := createActivity()
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/activities/"+itoa(activity.ID), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

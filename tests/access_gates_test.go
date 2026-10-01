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

// accessGateRoutes are the Company/Contact and top-level Quote/Payment/
// Contract routes gated on salesPipelineRoles (spec §1.7). The ids don't
// exist: a gated role gets 403 before the handler, any other role 404.
var accessGateRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/companies"},
	{http.MethodPost, "/api/v1/companies"},
	{http.MethodGet, "/api/v1/companies/999999"},
	{http.MethodPut, "/api/v1/companies/999999"},
	{http.MethodGet, "/api/v1/companies/999999/products"},
	{http.MethodGet, "/api/v1/companies/999999/projects"},
	{http.MethodGet, "/api/v1/contacts"},
	{http.MethodPost, "/api/v1/contacts"},
	{http.MethodGet, "/api/v1/contacts/999999"},
	{http.MethodPut, "/api/v1/contacts/999999"},
	{http.MethodPatch, "/api/v1/customer-products/999999"},
	{http.MethodPut, "/api/v1/quotes/999999"},
	{http.MethodDelete, "/api/v1/quotes/999999"},
	{http.MethodGet, "/api/v1/quotes/999999/export-pdf"},
	{http.MethodPost, "/api/v1/quotes/999999/duplicate"},
	{http.MethodPut, "/api/v1/payments/999999"},
	{http.MethodDelete, "/api/v1/payments/999999"},
	{http.MethodPut, "/api/v1/payment-installments/999999"},
	{http.MethodDelete, "/api/v1/payment-installments/999999"},
	{http.MethodPut, "/api/v1/contracts/999999"},
	{http.MethodPost, "/api/v1/contracts/999999/upload"},
	{http.MethodGet, "/api/v1/contracts/999999/export-pdf"},
}

func TestAccess_ProductionForbidden(t *testing.T) {
	app, db := testutil.App(t)
	prod := testutil.CreateUser(t, db, models.RoleProduction)

	for _, r := range accessGateRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := testutil.AuthRequest(t, r.method, r.path, map[string]interface{}{}, prod.ID, prod.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}

	// Production's own Projects page still works; rows carry company_name.
	t.Run("projects_list_ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/projects", nil, prod.ID, prod.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// Marketing has Sales Rep parity on Companies/Contacts and the Deal
// sub-resources: past the route gate (404 on a missing id, not 403).
func TestAccess_MarketingPassesGates(t *testing.T) {
	app, db := testutil.App(t)
	mkt := testutil.CreateUser(t, db, models.RoleMarketing)

	for _, r := range accessGateRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := testutil.AuthRequest(t, r.method, r.path, map[string]interface{}{}, mkt.ID, mkt.Role)
			resp := doJSON(t, app, req, nil)
			assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
		})
	}

	t.Run("company_list_ok", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies", nil, mkt.ID, mkt.Role)
		assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
	})
}

// Company/Contact Delete is Admin/Sales Manager only, like trash/restore.
func TestAccess_CompanyContactDeleteManagersOnly(t *testing.T) {
	app, db := testutil.App(t)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	for _, role := range []models.Role{models.RoleSalesRep, models.RoleMarketing, models.RoleProduction} {
		u := testutil.CreateUser(t, db, role)
		for _, path := range []string{"/api/v1/companies/" + itoa(company.ID), "/api/v1/contacts/" + itoa(contact.ID)} {
			req := testutil.AuthRequest(t, http.MethodDelete, path, nil, u.ID, u.Role)
			assert.Equal(t, http.StatusForbidden, doJSON(t, app, req, nil).StatusCode, "%s DELETE %s", role, path)
		}
	}

	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/contacts/"+itoa(contact.ID), nil, manager.ID, manager.Role)
	assert.Equal(t, http.StatusNoContent, doJSON(t, app, req, nil).StatusCode)
	req = testutil.AuthRequest(t, http.MethodDelete, "/api/v1/companies/"+itoa(company.ID), nil, manager.ID, manager.Role)
	assert.Equal(t, http.StatusNoContent, doJSON(t, app, req, nil).StatusCode)
}

func TestAccess_CompanyDeleteBlockedByActiveDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	path := "/api/v1/companies/" + itoa(deal.CompanyID)

	del := func() int {
		return doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, path, nil, admin.ID, admin.Role), nil).StatusCode
	}

	assert.Equal(t, http.StatusConflict, del(), "open deal")

	require.NoError(t, db.Model(deal).Update("status", models.DealStatusWon).Error)
	assert.Equal(t, http.StatusConflict, del(), "won deal")

	require.NoError(t, db.Model(deal).Update("status", models.DealStatusLost).Error)
	assert.Equal(t, http.StatusNoContent, del(), "only a lost deal")

	// A soft-deleted open deal doesn't block either.
	other := seedDeal(t, db, nil)
	require.NoError(t, db.Delete(other).Error)
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/companies/"+itoa(other.CompanyID), nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	var company models.Company
	assert.ErrorIs(t, db.First(&company, deal.CompanyID).Error, gorm.ErrRecordNotFound)
}

func TestAccess_CompanyContactDeleteRestoreAudited(t *testing.T) {
	app, db := testutil.App(t)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	cases := []struct {
		entity string
		id     uint
		base   string
		name   string
	}{
		{"contact", contact.ID, "/api/v1/contacts/", contact.Name},
		{"company", company.ID, "/api/v1/companies/", company.Name},
	}
	for _, tc := range cases {
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, tc.base+itoa(tc.id), nil, manager.ID, manager.Role), nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode, tc.entity)
		resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.base+itoa(tc.id)+"/restore", nil, manager.ID, manager.Role), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, tc.entity)

		var rows []models.AuditLogEntry
		require.NoError(t, db.Where("entity_type = ? AND entity_id = ?", tc.entity, tc.id).Order("id").Find(&rows).Error)
		require.Len(t, rows, 2, tc.entity)
		assert.Equal(t, "deleted", rows[0].Action)
		assert.Equal(t, "restored", rows[1].Action)
		for _, r := range rows {
			assert.Equal(t, manager.ID, r.ActorID)
		}
		assert.Equal(t, tc.name, rows[0].Before["name"], tc.entity)
	}
}

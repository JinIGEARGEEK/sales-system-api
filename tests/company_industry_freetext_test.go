package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestCompanyCreate_IndustryFreeTextAutoRegisters guards the fix replacing
// CompanyHandler.Create/Update's reject-if-unknown industry check with
// utils.EnsureActiveIndustry: a brand-new, never-seen industry string must
// save the Company (not 422) and register itself as a new active
// IndustryOption row for future reuse/curation via /admin/industries.
func TestCompanyCreate_IndustryFreeTextAutoRegisters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	const custom = "Quantum Widgetry"
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies", map[string]interface{}{
		"name": "Test Co", "industry": custom,
	}, admin.ID, admin.Role)
	var out struct {
		Data models.Company `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "a never-seen industry must not be rejected")
	assert.Equal(t, custom, out.Data.Industry)

	var opt models.IndustryOption
	require.NoError(t, db.Where("name = ?", custom).First(&opt).Error, "the industry must be auto-registered")
	assert.True(t, opt.IsActive)
}

// TestCompanyUpdate_ReactivatesDeactivatedIndustry guards
// EnsureActiveIndustry's reactivation path: an Admin deactivating an
// IndustryOption via /admin/industries must not permanently block a
// Company from being saved with that same industry name again later.
func TestCompanyUpdate_ReactivatesDeactivatedIndustry(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	opt := models.IndustryOption{Name: "Legacy Widgets", IsActive: true}
	require.NoError(t, db.Create(&opt).Error)

	deactivateReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/admin/industries/"+itoa(opt.ID), nil, admin.ID, admin.Role)
	resp := doJSON(t, app, deactivateReq, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	company := &models.Company{Name: "Widget Co", Status: models.StatusActive}
	require.NoError(t, db.Create(company).Error)

	updateReq := testutil.AuthRequest(t, http.MethodPut, "/api/v1/companies/"+itoa(company.ID), map[string]interface{}{
		"name": company.Name, "industry": opt.Name,
	}, admin.ID, admin.Role)
	resp = doJSON(t, app, updateReq, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.IndustryOption
	require.NoError(t, db.First(&reloaded, opt.ID).Error)
	assert.True(t, reloaded.IsActive, "saving a Company with a deactivated industry's name must reactivate it")
}

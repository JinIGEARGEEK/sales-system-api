package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestProspectCreate_LinksCompanyID guards Prospect.company_id round-tripping
// through Create as a real FK, same shape as Lead's.
func TestProspectCreate_LinksCompanyID(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	company := seedCompany(t, db)

	var out struct {
		Data models.Prospect `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Riley Chen", "company_id": company.ID, "source": "Website",
	}, marketing.ID, marketing.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out.Data.CompanyID)
	assert.Equal(t, company.ID, *out.Data.CompanyID)
	assert.Equal(t, models.ProspectStatusNew, out.Data.Status, "defaults to New when omitted")
}

// TestProspectList_RequiresProspectRole guards that a Sales Rep (no legitimate
// reason to see the pre-Lead marketing funnel) is forbidden, matching the
// route-level RequireRoles(Admin, Marketing, Sales Manager) gate.
func TestProspectList_RequiresProspectRole(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/prospects", nil, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestProspectConvert_CreatesLeadAndReusesCompany guards the core Convert
// path: reuses the Prospect's own linked Company (no duplicate), creates a
// Lead back-referencing the Prospect, and flips the Prospect to Converted —
// mirrors TestLeadConvert_ReusesLeadsExistingCompany's Lead→Deal coverage one
// funnel stage earlier.
func TestProspectConvert_CreatesLeadAndReusesCompany(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	company := &models.Company{Name: "Initrode", Status: models.StatusActive}
	require.NoError(t, db.Create(company).Error)
	prospect := seedProspect(t, db, &company.ID)

	var out struct {
		Data struct {
			Lead    models.Lead    `json:"lead"`
			Company models.Company `json:"company"`
			Contact models.Contact `json:"contact"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert", map[string]interface{}{},
		marketing.ID, marketing.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, company.ID, out.Data.Company.ID, "convert must reuse the Prospect's own linked Company")
	require.NotNil(t, out.Data.Lead.CompanyID)
	assert.Equal(t, company.ID, *out.Data.Lead.CompanyID)
	require.NotNil(t, out.Data.Lead.ProspectID)
	assert.Equal(t, prospect.ID, *out.Data.Lead.ProspectID)
	assert.Equal(t, models.LeadStatusNew, out.Data.Lead.Status)
	assert.NotZero(t, out.Data.Contact.ID, "a Contact should be created from the prospect's name/email/phone")
	assert.Equal(t, prospect.Name, out.Data.Contact.Name)

	var companyCount int64
	require.NoError(t, db.Model(&models.Company{}).Where("name = ?", "Initrode").Count(&companyCount).Error)
	assert.Equal(t, int64(1), companyCount, "must not create a duplicate Company for the same Prospect")

	var updated models.Prospect
	require.NoError(t, db.First(&updated, prospect.ID).Error)
	assert.Equal(t, models.ProspectStatusConverted, updated.Status)
	require.NotNil(t, updated.ConvertedLeadID)
	assert.Equal(t, out.Data.Lead.ID, *updated.ConvertedLeadID)
}

// TestProspectConvert_ExplicitCompanyIDOverridesProspectsOwnLink mirrors
// TestLeadConvert_ExplicitCompanyIDOverridesLeadsOwnLink: a caller-supplied
// company_id on the Convert request wins over the Prospect's own link.
func TestProspectConvert_ExplicitCompanyIDOverridesProspectsOwnLink(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	wrongCompany := &models.Company{Name: "Wrong Co", Status: models.StatusActive}
	require.NoError(t, db.Create(wrongCompany).Error)
	rightCompany := &models.Company{Name: "Right Co", Status: models.StatusActive}
	require.NoError(t, db.Create(rightCompany).Error)
	prospect := seedProspect(t, db, &wrongCompany.ID)

	var out struct {
		Data struct {
			Lead models.Lead `json:"lead"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert", map[string]interface{}{
		"company_id": rightCompany.ID,
	}, marketing.ID, marketing.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, out.Data.Lead.CompanyID)
	assert.Equal(t, rightCompany.ID, *out.Data.Lead.CompanyID)
}

// TestProspectConvert_RejectsDoubleConversion guards the ConvertedLeadID
// guard — converting an already-converted Prospect must 409, not silently
// create a second Lead.
func TestProspectConvert_RejectsDoubleConversion(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	prospect := seedProspect(t, db, nil)

	first := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert", map[string]interface{}{},
		marketing.ID, marketing.Role)
	resp := doJSON(t, app, first, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	second := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert", map[string]interface{}{},
		marketing.ID, marketing.Role)
	resp = doJSON(t, app, second, nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

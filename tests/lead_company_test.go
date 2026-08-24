package apitests

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// seedLead creates a Lead, optionally linked to a Company (nil = unlinked,
// mirroring seedDeal's assignedTo *uint nilable-param convention).
func seedLead(t *testing.T, db *gorm.DB, companyID *uint) *models.Lead {
	t.Helper()
	lead := &models.Lead{
		Name: "Jordan Lee", CompanyID: companyID,
		Source: models.LeadSourceWebsite, Status: models.LeadStatusQualified,
	}
	require.NoError(t, db.Create(lead).Error)
	return lead
}

// TestLeadCreate_LinksCompanyID guards the 2026-08-24 migration off free-text
// company_name: a created Lead's company_id round-trips through Create as a
// real FK, not a string.
func TestLeadCreate_LinksCompanyID(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	var out struct {
		Data models.Lead `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Jordan Lee", "company_id": company.ID, "source": "Website",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out.Data.CompanyID)
	assert.Equal(t, company.ID, *out.Data.CompanyID)
}

// TestLeadUpdate_ChangesCompanyID guards Update accepting company_id the
// same way Create does.
func TestLeadUpdate_ChangesCompanyID(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	original := seedCompany(t, db)
	lead := seedLead(t, db, &original.ID)

	newCompany := &models.Company{Name: "Globex Corp", Status: models.StatusActive}
	require.NoError(t, db.Create(newCompany).Error)

	var out struct {
		Data models.Lead `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
		"name": lead.Name, "company_id": newCompany.ID, "source": "Website", "status": "Qualified",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, out.Data.CompanyID)
	assert.Equal(t, newCompany.ID, *out.Data.CompanyID)
}

// TestLeadConvert_ReusesLeadsExistingCompany guards the dedupe fix noted in
// biz_spec/feature-spec.md's FR-CRM-001 update: converting a Lead that's
// already linked to a real Company (via CompanyID, set at create/edit time)
// must reuse that exact Company, not create a fresh duplicate from its name
// — the bug this convert path used to have when Lead only carried a
// free-text company_name.
func TestLeadConvert_ReusesLeadsExistingCompany(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := &models.Company{Name: "Initech", Status: models.StatusActive}
	require.NoError(t, db.Create(company).Error)
	lead := seedLead(t, db, &company.ID)

	var out struct {
		Data struct {
			Deal    models.Deal    `json:"deal"`
			Company models.Company `json:"company"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"deal": map[string]interface{}{"title": "Initech Deal", "value": 5000, "stage": "Lead"},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, company.ID, out.Data.Company.ID, "convert must reuse the Lead's own linked Company")
	assert.Equal(t, company.ID, out.Data.Deal.CompanyID)

	var companyCount int64
	require.NoError(t, db.Model(&models.Company{}).Where("name = ?", "Initech").Count(&companyCount).Error)
	assert.Equal(t, int64(1), companyCount, "must not create a duplicate Company for the same Lead")
}

// TestLeadConvert_FallsBackWhenLeadsLinkedCompanyWasDeleted guards that
// Convert doesn't 500 when the Lead's own CompanyID points at a Company
// that's since been soft-deleted (this id was never caller-supplied on the
// convert request itself, unlike an explicit company_id override) — it
// should fall back to creating a fresh Company, the same as a Lead with no
// company at all, rather than failing the whole conversion.
func TestLeadConvert_FallsBackWhenLeadsLinkedCompanyWasDeleted(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := &models.Company{Name: "Soon Deleted Co", Status: models.StatusActive}
	require.NoError(t, db.Create(company).Error)
	lead := seedLead(t, db, &company.ID)
	require.NoError(t, db.Delete(company).Error)

	var out struct {
		Data struct {
			Deal    models.Deal    `json:"deal"`
			Company models.Company `json:"company"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"deal": map[string]interface{}{"title": "Fallback Deal", "value": 1000, "stage": "Lead"},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEqual(t, company.ID, out.Data.Company.ID, "must not reuse the soft-deleted Company")
	assert.Equal(t, out.Data.Company.ID, out.Data.Deal.CompanyID)
}

// TestLeadConvert_ExplicitCompanyIDOverridesLeadsOwnLink guards that a
// caller-supplied company_id on the Convert request still wins over
// whatever Company the Lead itself was already linked to — e.g. a rep
// correcting a mis-linked Lead at convert time.
func TestLeadConvert_ExplicitCompanyIDOverridesLeadsOwnLink(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	leadCompany := &models.Company{Name: "Wrong Co", Status: models.StatusActive}
	require.NoError(t, db.Create(leadCompany).Error)
	correctCompany := &models.Company{Name: "Right Co", Status: models.StatusActive}
	require.NoError(t, db.Create(correctCompany).Error)
	lead := seedLead(t, db, &leadCompany.ID)

	var out struct {
		Data struct {
			Deal models.Deal `json:"deal"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"company_id": correctCompany.ID,
		"deal":       map[string]interface{}{"title": "Right Co Deal", "value": 5000, "stage": "Lead"},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, correctCompany.ID, out.Data.Deal.CompanyID)
}

// TestLeadList_FiltersAndSortsByCompany guards List's company_id-related
// behavior post-migration: an exact company_id filter, a join-based sort by
// the related Company's name (same mechanism as before, since Lead.company_id
// isn't a plain sortable column), and — unlike Deal/Contact's own company
// search, which was never text-based — "search" still matches by company
// name too, now via a join instead of the dropped free-text column.
func TestLeadList_FiltersAndSortsByCompany(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	companyA := &models.Company{Name: "Aardvark Inc", Status: models.StatusActive}
	require.NoError(t, db.Create(companyA).Error)
	companyZ := &models.Company{Name: "Zebra LLC", Status: models.StatusActive}
	require.NoError(t, db.Create(companyZ).Error)
	leadA := seedLead(t, db, &companyA.ID)
	leadZ := seedLead(t, db, &companyZ.ID)

	t.Run("company_id filter", func(t *testing.T) {
		var out struct {
			Data []models.Lead `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads?company_id="+itoa(companyA.ID), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 1)
		assert.Equal(t, leadA.ID, out.Data[0].ID)
	})

	t.Run("sort by company_name ascending", func(t *testing.T) {
		var out struct {
			Data []models.Lead `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads?sort=company_name", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 2)
		assert.Equal(t, leadA.ID, out.Data[0].ID, "Aardvark Inc sorts before Zebra LLC")
		assert.Equal(t, leadZ.ID, out.Data[1].ID)
	})

	t.Run("search matches company name", func(t *testing.T) {
		var out struct {
			Data []models.Lead `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads?search=zebra", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, out.Data, 1)
		assert.Equal(t, leadZ.ID, out.Data[0].ID)
	})

	t.Run("search still matches leads with no company at all", func(t *testing.T) {
		unlinked := seedLead(t, db, nil)
		var out struct {
			Data []models.Lead `json:"data"`
		}
		// Every seedLead fixture shares the name "Jordan Lee", so this also
		// matches leadA/leadZ — the point is specifically that `unlinked`
		// (company_id IS NULL) still comes back at all, proving the join
		// is a LEFT JOIN and not an inner one.
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads?search="+url.QueryEscape(unlinked.Name), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		ids := make([]uint, len(out.Data))
		for i, l := range out.Data {
			ids[i] = l.ID
		}
		assert.Contains(t, ids, unlinked.ID)
	})
}

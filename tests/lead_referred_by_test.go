package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestLeadCreate_LinksReferredByCompany guards referred_by_type/referred_by_id
// round-tripping through Create when the referrer is an existing Company.
func TestLeadCreate_LinksReferredByCompany(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	var out struct {
		Data models.Lead `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Referred Lead", "source": "Referral",
		"referred_by_type": "company", "referred_by_id": company.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out.Data.ReferredByType)
	require.NotNil(t, out.Data.ReferredByID)
	assert.Equal(t, models.RelatedTypeCompany, *out.Data.ReferredByType)
	assert.Equal(t, company.ID, *out.Data.ReferredByID)
}

// TestLeadCreate_LinksReferredByContact is the Contact-branch sibling of the
// Company case above.
func TestLeadCreate_LinksReferredByContact(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	var out struct {
		Data models.Lead `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Referred Lead", "source": "Referral",
		"referred_by_type": "contact", "referred_by_id": contact.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out.Data.ReferredByType)
	assert.Equal(t, models.RelatedTypeContact, *out.Data.ReferredByType)
	assert.Equal(t, contact.ID, *out.Data.ReferredByID)
}

// TestLeadCreate_RejectsInvalidReferredByType guards the company/contact-only
// restriction — Deal/Prospect aren't valid referrers even though they're
// otherwise-valid ActivityRelatedType values elsewhere in the app.
func TestLeadCreate_RejectsInvalidReferredByType(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Bad Referrer Lead", "source": "Referral",
		"referred_by_type": "deal", "referred_by_id": 1,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestLeadCreate_RejectsReferredByTypeWithoutID and its sibling below guard
// the both-or-neither rule — one set without the other is always invalid.
func TestLeadCreate_RejectsReferredByTypeWithoutID(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Half Referrer Lead", "source": "Referral",
		"referred_by_type": "company",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestLeadCreate_RejectsReferredByIDWithoutType(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Half Referrer Lead", "source": "Referral",
		"referred_by_id": company.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestLeadCreate_RejectsNonexistentReferredByID guards the new existence
// check — a well-formed type with an id that doesn't exist in that table at
// all was previously accepted unchecked.
func TestLeadCreate_RejectsNonexistentReferredByID(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "Bad Referrer Lead", "source": "Referral",
		"referred_by_type": "company", "referred_by_id": 999999,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestLeadCreate_AllowsNoReferrer confirms the new fields stay fully optional
// — most Leads (any non-Referral source) will never set them.
func TestLeadCreate_AllowsNoReferrer(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "No Referrer Lead", "source": "Website",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// TestLeadUpdate_ChangesReferredBy guards Update accepting referred_by_type/
// referred_by_id the same way Create does — mirrors TestLeadUpdate_ChangesCompanyID.
func TestLeadUpdate_ChangesReferredBy(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	lead := seedLead(t, db, nil)

	var out struct {
		Data models.Lead `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
		"name": lead.Name, "source": "Referral",
		"referred_by_type": "company", "referred_by_id": company.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, out.Data.ReferredByID)
	assert.Equal(t, company.ID, *out.Data.ReferredByID)
}

package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestTrashSearch_CompaniesFilterByName guards utils.GenericTrash's optional
// `search` query param (ILIKE against the caller-provided searchColumns) —
// Company's Trash passes "name".
func TestTrashSearch_CompaniesFilterByName(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	acme := &models.Company{Name: "Acme Corp", Status: models.StatusActive}
	require.NoError(t, db.Create(acme).Error)
	globex := &models.Company{Name: "Globex Inc", Status: models.StatusActive}
	require.NoError(t, db.Create(globex).Error)

	for _, id := range []uint{acme.ID, globex.ID} {
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/companies/"+itoa(id), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
	}

	var trash struct {
		Data []models.Company `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies/trash?search=acme", nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &trash)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, trash.Data, 1, "search=acme must exclude Globex")
	assert.Equal(t, acme.ID, trash.Data[0].ID)
}

// TestTrashSearch_DealsFilterByTitle covers the same GenericTrash search
// param on Deal's Trash, which passes "title" instead of "name".
func TestTrashSearch_DealsFilterByTitle(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	wanted := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", wanted.ID).Update("title", "Website Redesign").Error)
	other := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", other.ID).Update("title", "Mobile App").Error)

	for _, id := range []uint{wanted.ID, other.ID} {
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/deals/"+itoa(id), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
	}

	var trash struct {
		Data []models.Deal `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/trash?search=redesign", nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &trash)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, trash.Data, 1, "search=redesign must exclude Mobile App")
	assert.Equal(t, wanted.ID, trash.Data[0].ID)
}

// TestTrashSearch_OmittedReturnsEverything guards the no-op default: Trash
// endpoints whose caller passes no searchColumns (Prospect/User) must ignore
// a stray ?search= rather than erroring, and a Trash call with no ?search=
// at all must keep returning every deleted row (existing behavior unchanged).
func TestTrashSearch_OmittedReturnsEverything(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(target.ID), nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var trash struct {
		Data []models.User `json:"data"`
	}
	req = testutil.AuthRequest(t, http.MethodGet, "/api/v1/users/trash?search=anything", nil, admin.ID, admin.Role)
	resp = doJSON(t, app, req, &trash)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, trash.Data, 1, "User's Trash passes no searchColumns, so ?search= must be a no-op")
	assert.Equal(t, target.ID, trash.Data[0].ID)
}

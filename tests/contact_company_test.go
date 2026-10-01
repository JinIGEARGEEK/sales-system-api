package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestContact_CompanyIDMustExist guards that Contact Create, and an Update
// that changes company_id, reject a company_id naming no live Company (422)
// instead of saving a Contact attached to nothing. An Update that resends
// the Contact's own company_id still works after that Company is deleted.
func TestContact_CompanyIDMustExist(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	type errBody struct {
		Error struct {
			Fields map[string][]string `json:"fields"`
		} `json:"error"`
	}

	var create errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/contacts",
		map[string]interface{}{"company_id": 999999, "name": "Orphan"}, admin.ID, admin.Role), &create)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, []string{"not_found"}, create.Error.Fields["company_id"])
	var n int64
	require.NoError(t, db.Model(&models.Contact{}).Where("name = ?", "Orphan").Count(&n).Error)
	assert.Zero(t, n, "nothing saved")

	contact := seedContact(t, db, company.ID)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contacts/"+itoa(contact.ID),
		map[string]interface{}{"company_id": 999999, "name": "Jane Doe"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "moving to a missing Company")

	require.NoError(t, db.Delete(company).Error)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contacts/"+itoa(contact.ID),
		map[string]interface{}{"company_id": company.ID, "name": "Jane Renamed"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "an unchanged company_id isn't re-checked")
}

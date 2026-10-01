package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestCompanyRestore_DomainTakenIs409 guards that restoring a Company whose
// website domain another live Company has taken since is a 409 (as Create
// would answer), not a 500 from the unique domain index.
func TestCompanyRestore_DomainTakenIs409(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	create := func(name string) uint {
		var body struct {
			Data models.Company `json:"data"`
		}
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies",
			map[string]interface{}{"name": name, "website": "https://restore-clash.example"}, admin.ID, admin.Role), &body)
		require.Equal(t, fiber.StatusCreated, resp.StatusCode)
		return body.Data.ID
	}
	first := create("Restore Clash Original")
	require.Equal(t, fiber.StatusNoContent, doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete,
		"/api/v1/companies/"+itoa(first), nil, admin.ID, admin.Role), nil).StatusCode)
	create("Restore Clash Replacement")

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost,
		"/api/v1/companies/"+itoa(first)+"/restore", nil, admin.ID, admin.Role), nil)
	require.Equal(t, fiber.StatusConflict, resp.StatusCode)
}

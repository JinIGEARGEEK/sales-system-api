package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestTeamMembers_IncludesRole guards that /team-members carries each user's
// role, which assignee pickers use to leave out Production.
func TestTeamMembers_IncludesRole(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	production := testutil.CreateUser(t, db, models.RoleProduction)

	var body struct {
		Data []struct {
			ID   uint        `json:"id"`
			Role models.Role `json:"role"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/team-members", nil, rep.ID, rep.Role), &body)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	roles := map[uint]models.Role{}
	for _, m := range body.Data {
		roles[m.ID] = m.Role
	}
	require.Equal(t, models.RoleSalesRep, roles[rep.ID])
	require.Equal(t, models.RoleProduction, roles[production.ID])
}

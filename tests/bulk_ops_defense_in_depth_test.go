package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/handlers"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestBulkOps_DefenseInDepth_RejectsOtherRepsRecords guards bulk_ops.go's
// own per-row CanWrite check inside bulkReassignEntity/bulkTagEntity/
// bulkArchiveEntity — independent of routes.go's Admin/Sales-Manager-only
// bulkRoles gate, which is what actually protects these endpoints in
// production today (CanWrite is always true for those two roles, per
// middleware.IsManager). Routes.go's own gate makes this unreachable via the
// real HTTP routes with a plain Sales Rep (RequireRoles 403s first), so this
// mounts the handler directly with faked auth locals to prove the generic
// helpers themselves still fail closed — in case a future route ever wires
// one of these into a role that isn't Admin/Sales-Manager-gated, the way
// Task's own hand-written bulk endpoints already are (tasks.go).
func TestBulkOps_DefenseInDepth_RejectsOtherRepsRecords(t *testing.T) {
	_, db := testutil.App(t) // truncates + migrates; its own app is unused below
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	other := testutil.CreateUser(t, db, models.RoleSalesRep)

	asOther := func(app *fiber.App) {
		app.Use(func(c *fiber.Ctx) error {
			c.Locals(middleware.LocalUserID, other.ID)
			c.Locals(middleware.LocalRole, other.Role)
			return c.Next()
		})
	}

	t.Run("BulkArchive", func(t *testing.T) {
		deal := seedDeal(t, db, &owner.ID)
		app := fiber.New()
		asOther(app)
		app.Patch("/x", handlers.NewDealHandler(db).BulkArchive)

		req := testutil.NewRequest(t, http.MethodPatch, "/x", map[string]interface{}{"ids": []uint{deal.ID}}, "")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusForbidden, resp.StatusCode)

		var count int64
		db.Model(&models.Deal{}).Where("id = ? AND deleted_at IS NULL", deal.ID).Count(&count)
		require.Equal(t, int64(1), count, "the Deal must be untouched")
	})

	t.Run("BulkTag", func(t *testing.T) {
		deal := seedDeal(t, db, &owner.ID)
		app := fiber.New()
		asOther(app)
		app.Patch("/x", handlers.NewDealHandler(db).BulkTag)

		req := testutil.NewRequest(t, http.MethodPatch, "/x", map[string]interface{}{
			"ids": []uint{deal.ID}, "tags": []string{"stolen"},
		}, "")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusForbidden, resp.StatusCode)

		var got models.Deal
		require.NoError(t, db.First(&got, deal.ID).Error)
		require.Empty(t, []string(got.Tags), "the Deal's tags must be untouched")
	})

	t.Run("BulkReassign", func(t *testing.T) {
		deal := seedDeal(t, db, &owner.ID)
		app := fiber.New()
		asOther(app)
		app.Patch("/x", handlers.NewDealHandler(db).BulkReassign)

		req := testutil.NewRequest(t, http.MethodPatch, "/x", map[string]interface{}{
			"ids": []uint{deal.ID}, "assigned_to": other.ID,
		}, "")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusForbidden, resp.StatusCode)

		var got models.Deal
		require.NoError(t, db.First(&got, deal.ID).Error)
		require.NotNil(t, got.AssignedTo)
		require.Equal(t, owner.ID, *got.AssignedTo, "the Deal must still belong to its original owner")
	})
}

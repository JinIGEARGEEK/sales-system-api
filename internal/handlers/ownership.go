package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/igeargeek/sales-system-api/internal/middleware"
)

// CanWrite implements §1.7's Sales Rep scope: full CRUD on records assigned to
// them or unassigned; Admin/Sales Manager can write anything.
func CanWrite(c *fiber.Ctx, assignedTo *uint) bool {
	if middleware.IsManager(c) {
		return true
	}
	return assignedTo == nil || *assignedTo == middleware.CurrentUserID(c)
}

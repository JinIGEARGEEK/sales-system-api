package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// errForbidden lets a resource-loading helper (e.g. dealForSubResource) distinguish
// "not found" from "found but not yours to write" without a second return value
// at every call site.
var errForbidden = errors.New("forbidden")

// CanWrite implements §1.7's Sales Rep scope: full CRUD on records assigned to
// them or unassigned; Admin/Sales Manager can write anything.
func CanWrite(c *fiber.Ctx, assignedTo *uint) bool {
	if middleware.IsManager(c) {
		return true
	}
	return assignedTo == nil || *assignedTo == middleware.CurrentUserID(c)
}

// respondFindErr maps errForbidden/gorm-not-found from a loader helper to the
// right HTTP status, so call sites don't need to know which occurred.
func respondFindErr(c *fiber.Ctx, err error, notFoundMsg string) error {
	if errors.Is(err, errForbidden) {
		return utils.Forbidden(c, "Not authorized to modify this deal's records")
	}
	return utils.NotFound(c, notFoundMsg)
}

package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
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

// errInvalidAssignee is returned by validateAssignee when assigned_to doesn't
// name an active user in a sales-pipeline role; callers answer 422 on field
// "assigned_to".
var errInvalidAssignee = errors.New("assigned_to must be an active user in a sales role")

// validateAssignee checks that id (when set) is an existing, active user whose
// role may own pipeline records (models.SalesPipelineRoles). nil is valid.
func validateAssignee(db *gorm.DB, id *uint) error {
	if id == nil {
		return nil
	}
	var n int64
	if err := db.Model(&models.User{}).
		Where("id = ? AND is_active = ? AND role IN ?", *id, true, models.SalesPipelineRoles).
		Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errInvalidAssignee
	}
	return nil
}

// respondAssigneeErr writes the response for a validateAssignee error.
func respondAssigneeErr(c *fiber.Ctx, err error) error {
	if errors.Is(err, errInvalidAssignee) {
		return utils.ValidationError(c, err.Error(), map[string][]string{"assigned_to": {"invalid"}})
	}
	return utils.Internal(c, "Failed to validate assignee")
}

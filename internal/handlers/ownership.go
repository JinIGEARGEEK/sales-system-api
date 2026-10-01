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

// respondFindErr maps errForbidden/gorm-not-found from a Deal sub-resource
// loader (dealForSubResource and friends) to the right HTTP status, so call
// sites don't need to know which occurred.
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

// CanSetAssignee is the PUT-time rule for changing a record's assigned_to:
// Admin/Sales Manager may set anything; anyone else may leave it unchanged
// or claim it for themselves, but not hand it to someone else or unassign it.
func CanSetAssignee(c *fiber.Ctx, current, next *uint) bool {
	if middleware.IsManager(c) || sameAssignee(current, next) {
		return true
	}
	return next != nil && *next == middleware.CurrentUserID(c)
}

// sameAssignee reports whether two nullable assigned_to values are equal.
func sameAssignee(a, b *uint) bool {
	return utils.UintPtrEqual(a, b)
}

// checkNewAssignee applies the incoming-assignee rules a write that sets
// assigned_to shares: CanWrite on next (a Sales Rep may assign only to
// themselves or leave it unassigned; 403 with forbiddenMsg), then, when
// next differs from current, validateAssignee (422 on assigned_to). An
// unchanged assignee isn't re-validated, so a record still owned by a
// since-deactivated user stays editable. Pass current nil on a create.
//
// Returns utils.ErrHandled once a response is written; the caller should
// `return nil`.
func checkNewAssignee(c *fiber.Ctx, db *gorm.DB, current, next *uint, forbiddenMsg string) error {
	if !CanWrite(c, next) {
		_ = utils.Forbidden(c, forbiddenMsg)
		return utils.ErrHandled
	}
	if sameAssignee(current, next) {
		return nil
	}
	if err := validateAssignee(db, next); err != nil {
		_ = respondAssigneeErr(c, err)
		return utils.ErrHandled
	}
	return nil
}

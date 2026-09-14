package handlers

import (
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// This file holds the three bulk-operation shapes shared by DealHandler,
// LeadHandler, and ProspectHandler's own BulkReassign/BulkTag/BulkArchive —
// previously each handler defined its own copy of all three, identical
// except for the model type, entity-name string, and the accessor for the
// one field being mutated. get/set accessor closures let one generic
// implementation reach each type's own AssignedTo/Tags field — Go generics
// can't do struct-field access by name, so this is the lightest-weight way
// to share the loop/transaction/audit-log shape without requiring every
// caller to implement a shared interface.
//
// Every one of the three checks CanWrite per row, exactly like each
// resource's own single-record Update/Delete does — even though today's
// only callers (Deal/Lead/Prospect) route these through routes.go's
// Admin/Sales-Manager-only bulkRoles gate, where CanWrite is always true
// (middleware.IsManager). That route-level gate is what actually protects
// these endpoints right now; the check here is defense in depth so the
// generic helpers themselves are correct independent of who calls them —
// if a future caller ever wires one of these into a route open to a plain
// Sales Rep (the way Task's own hand-written bulk endpoints already are,
// see tasks.go), it fails closed instead of silently having no ownership
// check at all.
func bulkReassignEntity[T any](c *fiber.Ctx, db *gorm.DB, entityType string,
	getAssignedTo func(*T) *uint, setAssignedTo func(*T, *uint)) error {
	var form bulkReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.ValidateBulkIDCount(c, form.IDs) {
		return nil
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, fmt.Sprintf("Cannot assign a %s to another team member", entityType))
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(db, form.IDs, entityType, "bulk_reassigned", actorID,
		func(tx *gorm.DB, item *T) (models.JSONMap, models.JSONMap, error) {
			if !CanWrite(c, getAssignedTo(item)) {
				return nil, nil, errForbidden
			}
			before := models.JSONMap{"assigned_to": getAssignedTo(item)}
			setAssignedTo(item, form.AssignedTo)
			after := models.JSONMap{"assigned_to": getAssignedTo(item)}
			return before, after, tx.Save(item).Error
		})
	if err != nil {
		if errors.Is(err, errForbidden) {
			return utils.Forbidden(c, fmt.Sprintf("Not authorized to reassign one or more of these %ss", entityType))
		}
		return utils.Internal(c, fmt.Sprintf("Failed to bulk reassign %ss", entityType))
	}
	return utils.NoContent(c)
}

// bulkTagEntity is the shared implementation behind Deal/Lead/Prospect's own
// BulkTag — mode "set" (default "add"/merge) replaces each row's tags
// outright, otherwise form.Tags is merged in via mergeTags. See
// bulkReassignEntity's doc above for why this checks CanWrite per row.
func bulkTagEntity[T any](c *fiber.Ctx, db *gorm.DB, entityType string,
	getAssignedTo func(*T) *uint, getTags func(*T) []string, setTags func(*T, []string)) error {
	var form bulkTagForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.ValidateBulkIDCount(c, form.IDs) {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(db, form.IDs, entityType, "bulk_tagged", actorID,
		func(tx *gorm.DB, item *T) (models.JSONMap, models.JSONMap, error) {
			if !CanWrite(c, getAssignedTo(item)) {
				return nil, nil, errForbidden
			}
			before := models.JSONMap{"tags": getTags(item)}
			if form.Mode == "set" {
				setTags(item, form.Tags)
			} else {
				setTags(item, mergeTags(getTags(item), form.Tags))
			}
			after := models.JSONMap{"tags": getTags(item)}
			return before, after, tx.Save(item).Error
		})
	if err != nil {
		if errors.Is(err, errForbidden) {
			return utils.Forbidden(c, fmt.Sprintf("Not authorized to tag one or more of these %ss", entityType))
		}
		return utils.Internal(c, fmt.Sprintf("Failed to bulk tag %ss", entityType))
	}
	return utils.NoContent(c)
}

// bulkArchiveEntity is the shared implementation behind Deal/Lead/
// Prospect's own BulkArchive — soft-deletes every listed id (same effect as
// each resource's own single-record Delete) in one transaction. See
// bulkReassignEntity's doc above for why this checks CanWrite per row.
func bulkArchiveEntity[T any](c *fiber.Ctx, db *gorm.DB, entityType string, getAssignedTo func(*T) *uint) error {
	var form bulkIDsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.ValidateBulkIDCount(c, form.IDs) {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(db, form.IDs, entityType, "bulk_archived", actorID,
		func(tx *gorm.DB, item *T) (models.JSONMap, models.JSONMap, error) {
			if !CanWrite(c, getAssignedTo(item)) {
				return nil, nil, errForbidden
			}
			if err := tx.Model(item).Update("deleted_by", actorID).Error; err != nil {
				return nil, nil, err
			}
			// No real "before" state to log here — every row this loop
			// visits is, by construction, not yet soft-deleted (BulkUpdate
			// only loads rows matching the default not-deleted scope), so a
			// fabricated `{"deleted_at": nil}` before-value carries no
			// information beyond "this row wasn't deleted yet," which is
			// always true. after.deleted_by is the only fact worth an
			// audit-log entry for.
			err := tx.Delete(item).Error
			return nil, models.JSONMap{"deleted_by": actorID}, err
		})
	if err != nil {
		if errors.Is(err, errForbidden) {
			return utils.Forbidden(c, fmt.Sprintf("Not authorized to archive one or more of these %ss", entityType))
		}
		return utils.Internal(c, fmt.Sprintf("Failed to bulk archive %ss", entityType))
	}
	return utils.NoContent(c)
}

package utils

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// GenericTrash lists Unscoped soft-deleted rows of T, newest-deleted first,
// through the same paginated envelope (List) every other list endpoint uses.
// Shared by Deal/Lead's GET /.../trash — identical apart from the model type
// and the "failed to list" message.
func GenericTrash[T any](c *fiber.Ctx, db *gorm.DB, failMsg string) error {
	page, perPage, offset := Pagination(c)
	query := db.Unscoped().Model(new(T)).Where("deleted_at IS NOT NULL")

	var total int64
	query.Count(&total)

	var items []T
	query = ApplySort(query, c.Query("sort"), map[string]bool{"deleted_at": true}, "-deleted_at")
	if err := query.Limit(perPage).Offset(offset).Find(&items).Error; err != nil {
		return Internal(c, failMsg)
	}
	return List(c, items, page, perPage, total)
}

// GenericRestore clears deleted_at/deleted_by on the Unscoped soft-deleted row
// of T identified by the ":id" param. Shared by Deal/Lead's POST
// /.../:id/restore — identical apart from the model type and messages.
func GenericRestore[T any](c *fiber.Ctx, db *gorm.DB, notFoundMsg, failMsg string) error {
	var item T
	if err := db.Unscoped().Where("deleted_at IS NOT NULL").First(&item, c.Params("id")).Error; err != nil {
		return NotFound(c, notFoundMsg)
	}
	if err := db.Unscoped().Model(&item).Updates(map[string]interface{}{"deleted_at": nil, "deleted_by": nil}).Error; err != nil {
		return Internal(c, failMsg)
	}
	return OK(c, item)
}

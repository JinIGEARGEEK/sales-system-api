package utils

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// GenericTrash lists Unscoped soft-deleted rows of T, newest-deleted first,
// through the same paginated envelope (List) every other list endpoint uses.
// Shared by Deal/Lead's GET /.../trash — identical apart from the model type
// and the "failed to list" message. `searchColumns` (optional) names the
// column(s) a `?search=` query param matches against (ILIKE, OR'd together)
// — e.g. Deal passes "title", Company/Contact/Lead pass "name". Omit it
// (Prospect/User's Trash callers do) to leave `search` a no-op, same as
// before this param existed.
func GenericTrash[T any](c *fiber.Ctx, db *gorm.DB, failMsg string, searchColumns ...string) error {
	page, perPage, offset := Pagination(c)
	query := db.Unscoped().Model(new(T)).Where("deleted_at IS NOT NULL")

	if search := c.Query("search"); search != "" && len(searchColumns) > 0 {
		like := "%" + search + "%"
		conds := make([]string, len(searchColumns))
		args := make([]interface{}, len(searchColumns))
		for i, col := range searchColumns {
			conds[i] = col + " ILIKE ?"
			args[i] = like
		}
		query = query.Where(strings.Join(conds, " OR "), args...)
	}

	var total int64
	query.Count(&total)

	var items []T
	query = ApplySort(query, c.Query("sort"), map[string]bool{"deleted_at": true}, "-deleted_at")
	if err := query.Limit(perPage).Offset(offset).Find(&items).Error; err != nil {
		return Internal(c, failMsg)
	}
	return List(c, items, page, perPage, total)
}

// GenericSoftDelete stamps deleted_by and soft-deletes item in one
// transaction. Previously every Delete handler (Company/Contact/Deal/Lead/User)
// ran these as two separate statements — a crash or error between them left
// deleted_by set with no deleted_at (or vice versa), and a failed second write
// after a committed first left the row inconsistently "half deleted" with no
// rollback. item must be a pointer to an already-loaded record (its ID is
// used for both writes).
func GenericSoftDelete(db *gorm.DB, item interface{}, actorID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(item).Update("deleted_by", actorID).Error; err != nil {
			return err
		}
		return tx.Delete(item).Error
	})
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

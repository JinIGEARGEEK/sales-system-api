package utils

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// GenericTrash lists Unscoped soft-deleted rows of T, newest-deleted first,
// through the same paginated envelope (List) every other list endpoint uses.
// searchColumns (optional) are the columns ?search= matches (ILIKE, OR'd
// together), e.g. "title" for Deals, "name" for Companies/Contacts/Leads;
// with none, search is ignored.
func GenericTrash[T any](c *fiber.Ctx, db *gorm.DB, failMsg string, searchColumns ...string) error {
	page, perPage, offset := Pagination(c)
	query := db.Unscoped().Model(new(T)).Where("deleted_at IS NOT NULL")

	if search := c.Query("search"); search != "" && len(searchColumns) > 0 {
		like := LikePattern(search)
		conds := make([]string, len(searchColumns))
		args := make([]interface{}, len(searchColumns))
		for i, col := range searchColumns {
			conds[i] = col + " ILIKE ? ESCAPE '\\'"
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
// transaction, so a row is never left with one set and not the other. item
// must be a pointer to an already-loaded record (its ID is used for both
// writes).
func GenericSoftDelete(db *gorm.DB, item interface{}, actorID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(item).Update("deleted_by", actorID).Error; err != nil {
			return err
		}
		return tx.Delete(item).Error
	})
}

// GenericRestore clears deleted_at/deleted_by on the Unscoped soft-deleted row
// of T identified by the ":id" param, for the POST /.../:id/restore routes
// that write no audit entry.
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

// GenericRestoreWithAudit is GenericRestore plus an entityType/"restored"
// audit entry written in the same transaction. id reads the row's primary key.
func GenericRestoreWithAudit[T any](c *fiber.Ctx, db *gorm.DB, entityType string, id func(*T) uint, actorID uint, notFoundMsg, failMsg string) error {
	var item T
	if err := db.Unscoped().Where("deleted_at IS NOT NULL").First(&item, c.Params("id")).Error; err != nil {
		return NotFound(c, notFoundMsg)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Model(&item).Updates(map[string]interface{}{"deleted_at": nil, "deleted_by": nil}).Error; err != nil {
			return err
		}
		return WriteAuditLog(tx, entityType, id(&item), "restored", nil, nil, actorID)
	})
	if err != nil {
		return Internal(c, failMsg)
	}
	return OK(c, item)
}

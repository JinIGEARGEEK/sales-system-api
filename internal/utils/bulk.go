package utils

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// BulkUpdate runs one transaction that loads each id in ids, hands the loaded
// row (and the same tx, so DB ops stay atomic) to apply, then writes one
// audit-log entry per row from apply's before/after. This is the shared
// "loop over ids, mutate, save, audit" shape behind Deal/Lead's
// BulkReassign/BulkTag/BulkArchive — they differ only in what apply does to
// each row.
func BulkUpdate[T any](db *gorm.DB, ids []uint, entityType, action string, actorID uint,
	apply func(tx *gorm.DB, item *T) (before, after models.JSONMap, err error)) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			var item T
			if err := tx.First(&item, id).Error; err != nil {
				return err
			}
			before, after, err := apply(tx, &item)
			if err != nil {
				return err
			}
			if err := WriteAuditLog(tx, entityType, id, action, before, after, actorID); err != nil {
				return err
			}
		}
		return nil
	})
}

package utils

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// MaxBulkIDs caps how many ids a single bulk-operation request (BulkReassign/
// BulkTag/BulkArchive/BulkMarkDone/...) may include in one call. BulkUpdate
// runs the whole batch inside one transaction, three statements per id (load,
// save/delete, audit-log write) — with no cap, a client posting a very large
// id list would hold that transaction (and its row locks) open for a long
// time, blocking other writers on the same rows. A caller with more ids to
// update than this should split the operation across multiple requests.
const MaxBulkIDs = 500

// ValidateBulkIDCount writes the appropriate 422 and returns false if ids is
// empty or exceeds MaxBulkIDs — the two checks every bulk-operation handler
// in this codebase performs before calling BulkUpdate. Callers should
// `return nil` immediately when this returns false, same convention as every
// other ValidationError-writing helper (e.g. validateExternalEmail).
func ValidateBulkIDCount(c *fiber.Ctx, ids []uint) bool {
	if len(ids) == 0 {
		_ = ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
		return false
	}
	if len(ids) > MaxBulkIDs {
		msg := fmt.Sprintf("must not exceed %d ids in one request", MaxBulkIDs)
		_ = ValidationError(c, "too many ids in one request", map[string][]string{"ids": {msg}})
		return false
	}
	return true
}

// BulkUpdate runs one transaction that loads each id in ids, hands the loaded
// row (and the same tx, so DB ops stay atomic) to apply, then writes one
// audit-log entry per row from apply's before/after. This is the shared
// "loop over ids, mutate, save, audit" shape behind Deal/Lead's
// BulkReassign/BulkTag/BulkArchive — they differ only in what apply does to
// each row.
func BulkUpdate[T any](db *gorm.DB, ids []uint, entityType, action string, actorID uint,
	apply func(tx *gorm.DB, item *T) (before, after models.JSONMap, err error)) error {
	ids = DedupeUints(ids)
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

// DedupeUints drops repeated ids, preserving first-seen order — a caller
// accidentally passing a duplicate id (e.g. "ids": [5, 5]) would otherwise
// have BulkUpdate load/apply/audit-log that row twice inside the same
// transaction, silently double-writing it and leaving two audit-log entries
// for one logical bulk action. Exported so other bulk-style handlers (e.g.
// CampaignHandler.BulkCreateTasks) can reuse the same duplicate-safety rule.
func DedupeUints(ids []uint) []uint {
	seen := make(map[uint]bool, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

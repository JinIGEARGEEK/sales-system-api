package utils

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// WriteAuditLog inserts an append-only AuditLogEntry — api-system-spec.md §8.5.
func WriteAuditLog(db *gorm.DB, entityType string, entityID uint, action string, before, after models.JSONMap, actorID uint) error {
	entry := models.AuditLogEntry{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     action,
		Before:     before,
		After:      after,
		ActorID:    actorID,
	}
	return db.Create(&entry).Error
}

// SaveWithAudit runs save inside a transaction and, when audit is true, writes
// a WriteAuditLog entry in the same transaction — the "persist a change, then
// record it" shape repeated across deals/products/projects status & reassign
// updates. Pass audit as e.g. `oldStatus != newStatus` for a conditional entry,
// or `true` for one that should always be written (e.g. a reassignment).
func SaveWithAudit(db *gorm.DB, save func(tx *gorm.DB) error, audit bool, entityType string, entityID uint, action string, before, after models.JSONMap, actorID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := save(tx); err != nil {
			return err
		}
		if audit {
			return WriteAuditLog(tx, entityType, entityID, action, before, after, actorID)
		}
		return nil
	})
}

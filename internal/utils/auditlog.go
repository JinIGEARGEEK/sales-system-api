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

package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type AuditLogHandler struct {
	DB *gorm.DB
}

func NewAuditLogHandler(db *gorm.DB) *AuditLogHandler {
	return &AuditLogHandler{DB: db}
}

// List — GET /audit-log (Admin only, route-gated). Append-only resource, no
// write handlers exist for it at all — NFR-007.
func (h *AuditLogHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.AuditLogEntry{})

	if v := c.Query("entity_type"); v != "" {
		query = query.Where("entity_type = ?", v)
	}
	if v := c.Query("entity_id"); v != "" {
		query = query.Where("entity_id = ?", v)
	}
	if v := c.Query("actor_id"); v != "" {
		query = query.Where("actor_id = ?", v)
	}
	if v := c.Query("date_from"); v != "" {
		query = query.Where("created_at >= ?", v)
	}
	if v := c.Query("date_to"); v != "" {
		query = query.Where("created_at <= ?", v)
	}

	var total int64
	query.Count(&total)

	var entries []models.AuditLogEntry
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&entries).Error; err != nil {
		return utils.Internal(c, "Failed to list audit log")
	}
	return utils.List(c, entries, page, perPage, total)
}

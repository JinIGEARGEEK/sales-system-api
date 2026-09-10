package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type AuditLogHandler struct {
	DB *gorm.DB
}

func NewAuditLogHandler(db *gorm.DB) *AuditLogHandler {
	return &AuditLogHandler{DB: db}
}

// List godoc
// @Summary List audit log entries (Admin/Sales Rep/Sales Manager)
// @Description Append-only audit log, read-only (NFR-007) — no write handlers exist for this resource at all. Non-Admin callers are hard-restricted server-side (not just route-gated) to a fixed slice of Deal history: entity_type=deal, ignoring any entity_type/actor_id they pass. Within that slice, allowed actions differ by role — Sales Rep sees stage_changed only; Sales Manager additionally sees reassigned/bulk_reassigned (the Deal detail page's "Owner History" card, FR-CRM-025/M-8). Admin has full reach across all entity types (settings, project, customer_product, deal, etc.) and may filter by entity_type/actor_id. entity_id/date_from/date_to apply to every role, so a non-Admin can still request a specific Deal's history or a date-bounded slice.
// @Tags audit-log
// @Security BearerAuth
// @Produce json
// @Param entity_type query string false "Filter by entity type (Admin only — ignored for Sales Rep/Sales Manager, who are hard-restricted to entity_type=deal)"
// @Param actor_id query string false "Filter by acting user ID (Admin only — ignored for Sales Rep/Sales Manager)"
// @Param entity_id query string false "Filter by entity ID (e.g. a specific Deal ID) — applies to all roles"
// @Param date_from query string false "ISO date lower bound (YYYY-MM-DD), filters on created_at"
// @Param date_to query string false "ISO date upper bound (YYYY-MM-DD), filters on created_at"
// @Param sort query string false "Sort field, prefix with - for descending (default -created_at)"
// @Param page query int false "Page number (default 1)"
// @Param per_page query int false "Items per page"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{} "Failed to list audit log"
// @Router /audit-log [get]
func (h *AuditLogHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.AuditLogEntry{})
	isAdmin := middleware.CurrentRole(c) == models.RoleAdmin
	isSalesManager := middleware.CurrentRole(c) == models.RoleSalesManager

	if isAdmin {
		if v := c.Query("entity_type"); v != "" {
			query = query.Where("entity_type = ?", v)
		}
		if v := c.Query("actor_id"); v != "" {
			query = query.Where("actor_id = ?", v)
		}
	} else if isSalesManager {
		query = query.Where("entity_type = ? AND action IN ?", "deal", []string{"stage_changed", "reassigned", "bulk_reassigned"})
	} else {
		query = query.Where("entity_type = ? AND action = ?", "deal", "stage_changed")
	}
	if v := c.Query("entity_id"); v != "" {
		query = query.Where("entity_id = ?", v)
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

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

// List — GET /audit-log, route-gated to Admin/Sales Rep/Sales Manager (see
// routes.go). Append-only resource, no write handlers exist for it at all —
// NFR-007.
//
// Non-Admin callers are hard-restricted here (not just route-gated) to Deal
// stage-change history only — entity_type=deal, action=stage_changed,
// ignoring any entity_type/actor_id/action they pass — so a Sales Rep/
// Manager can pull a Deal's pipeline history into the Activities pages as
// read-only context (the frontend request that motivated opening this route
// up at all) without gaining the Admin audit viewer's full reach into other
// entity types (settings, project, customer_product), other actions on a
// Deal (reassigned/bulk_reassigned — deliberately kept Admin-only, same as
// the Deal detail page's own "Owner History" card), or browsing by actor_id.
// entity_id/date_from/date_to still apply for non-Admins so a specific
// Deal's history (or a date-bounded slice of all Deals') can be requested.
func (h *AuditLogHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.AuditLogEntry{})
	isAdmin := middleware.CurrentRole(c) == models.RoleAdmin

	if isAdmin {
		if v := c.Query("entity_type"); v != "" {
			query = query.Where("entity_type = ?", v)
		}
		if v := c.Query("actor_id"); v != "" {
			query = query.Where("actor_id = ?", v)
		}
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

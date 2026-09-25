package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// applyActivityFilters is GET /activities' filter block, shared by the plain
// list and the include_stage_changes feed's activities half.
func applyActivityFilters(query *gorm.DB, c *fiber.Ctx, relatedType, relatedID string) *gorm.DB {
	if relatedType != "" && relatedID != "" {
		query = query.Where("activities.related_type = ? AND activities.related_id = ?", relatedType, relatedID)
	} else if relatedType != "" {
		query = query.Where("activities.related_type = ?", relatedType)
	}
	if v := c.Query("type"); v != "" {
		query = query.Where("activities.type = ?", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		args := append([]interface{}{like, like}, relatedRecordNameArgs(like)...)
		query = query.Where("activities.subject ILIKE ? OR activities.notes ILIKE ? OR "+relatedRecordNameMatch("activities"), args...)
	}
	return query
}

// canSeeStageHistory mirrors GET /audit-log's route gate (salesPipelineRoles):
// every role allowed there sees at least the deal/stage_changed slice, so the
// feed never exposes an audit row a role couldn't already read. Any other
// role asking for include_stage_changes just gets the plain Activity list.
func canSeeStageHistory(role models.Role) bool {
	switch role {
	case models.RoleAdmin, models.RoleSalesManager, models.RoleSalesRep, models.RoleMarketing:
		return true
	}
	return false
}

// StageChangeActivityType is the pseudo activity type the feed gives Deal
// stage-change rows, so `type=stage_change` narrows the feed to just them.
const StageChangeActivityType = "stage_change"

type activityFeedRow struct {
	ID          uint
	Kind        string
	Type        string
	Subject     string
	Notes       string
	RelatedType string
	RelatedID   uint
	CreatedByID uint
	CreatedAt   time.Time
	FromStage   string
	ToStage     string
}

// ActivityFeedItem is one row of the include_stage_changes feed. IDs are only
// unique per kind (activities and audit rows live in separate tables), so a
// client should key rows by kind+id.
type ActivityFeedItem struct {
	ID          uint      `json:"id"`
	Kind        string    `json:"kind"`
	Type        string    `json:"type"`
	Subject     string    `json:"subject"`
	Notes       string    `json:"notes"`
	RelatedType string    `json:"related_type"`
	RelatedID   uint      `json:"related_id"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	FromStage   string    `json:"from_stage,omitempty"`
	ToStage     string    `json:"to_stage,omitempty"`
}

// listFeed is GET /activities?include_stage_changes=true: real Activities
// plus Deal stage-change history (the "deal"/"stage_changed" audit rows GET
// /audit-log exposes to every pipeline role), UNION ALL'd into one list that
// is filtered, counted, sorted and paged in SQL — so the cross-entity
// Activities page can page server-side without losing that history. Stage
// rows come back as kind/type "stage_change", related_type "deal",
// related_id = the Deal, with from_stage/to_stage read from the audit row's
// before/after snapshots; real activities get kind "activity".
//
// Filters apply to both halves where they make sense: type=stage_change
// keeps only stage rows and any other type only activities; a related_type
// other than "deal" drops stage rows; related_id narrows stage rows to that
// Deal; search matches a stage row's from/to stage or its Deal's title.
func (h *ActivityHandler) listFeed(c *fiber.Ctx, relatedType, relatedID string, page, perPage, offset int) error {
	typeFilter := c.Query("type")
	parts := make([]*gorm.DB, 0, 2)

	if typeFilter != StageChangeActivityType {
		activities := applyActivityFilters(h.DB.Model(&models.Activity{}), c, relatedType, relatedID).
			Select("activities.id, 'activity' AS kind, activities.type::text AS type, activities.subject, activities.notes, " +
				"activities.related_type::text AS related_type, activities.related_id, activities.created_by_id, activities.created_at, " +
				"'' AS from_stage, '' AS to_stage")
		parts = append(parts, activities)
	}
	if (typeFilter == "" || typeFilter == StageChangeActivityType) && (relatedType == "" || relatedType == string(models.RelatedTypeDeal)) {
		stages := h.DB.Model(&models.AuditLogEntry{}).
			Select("audit_log_entries.id, 'stage_change' AS kind, 'stage_change' AS type, '' AS subject, '' AS notes, "+
				"'deal' AS related_type, audit_log_entries.entity_id AS related_id, audit_log_entries.actor_id AS created_by_id, "+
				"audit_log_entries.created_at, COALESCE(audit_log_entries.before->>'stage', '') AS from_stage, "+
				"COALESCE(audit_log_entries.after->>'stage', '') AS to_stage").
			Where("audit_log_entries.entity_type = ? AND audit_log_entries.action = ?", "deal", "stage_changed")
		if relatedID != "" {
			stages = stages.Where("audit_log_entries.entity_id = ?", relatedID)
		}
		if v := c.Query("search"); v != "" {
			like := "%" + v + "%"
			stages = stages.Where("audit_log_entries.after->>'stage' ILIKE ? OR audit_log_entries.before->>'stage' ILIKE ? OR "+
				"EXISTS (SELECT 1 FROM deals r WHERE r.id = audit_log_entries.entity_id AND r.title ILIKE ?)", like, like, like)
		}
		parts = append(parts, stages)
	}

	items := []ActivityFeedItem{}
	if len(parts) == 0 {
		return utils.List(c, items, page, perPage, 0)
	}
	union := "(?)"
	args := []interface{}{parts[0]}
	if len(parts) == 2 {
		union = "(?) UNION ALL (?)"
		args = append(args, parts[1])
	}

	var total int64
	if err := h.DB.Raw("SELECT COUNT(*) FROM ("+union+") AS feed", args...).Scan(&total).Error; err != nil {
		return utils.Internal(c, "Failed to list activities")
	}

	// Only created_at is sortable (as on the plain list); newest first by
	// default. kind/id break ties so paging is stable.
	dir := "DESC"
	if c.Query("sort") == "created_at" {
		dir = "ASC"
	}
	var rows []activityFeedRow
	pageArgs := append(append([]interface{}{}, args...), perPage, offset)
	if err := h.DB.Raw("SELECT * FROM ("+union+") AS feed ORDER BY created_at "+dir+", kind "+dir+", id "+dir+" LIMIT ? OFFSET ?", pageArgs...).
		Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to list activities")
	}

	names := h.userNames(rows)
	for _, r := range rows {
		item := ActivityFeedItem{
			ID: r.ID, Kind: r.Kind, Type: r.Type, Subject: r.Subject, Notes: r.Notes,
			RelatedType: r.RelatedType, RelatedID: r.RelatedID, CreatedBy: names[r.CreatedByID], CreatedAt: r.CreatedAt,
		}
		if r.Kind == StageChangeActivityType {
			item.FromStage, item.ToStage = r.FromStage, r.ToStage
		}
		items = append(items, item)
	}
	return utils.List(c, items, page, perPage, total)
}

func (h *ActivityHandler) userNames(rows []activityFeedRow) map[uint]string {
	idList := make([]uint, 0, len(rows))
	seen := make(map[uint]bool, len(rows))
	for _, r := range rows {
		if !seen[r.CreatedByID] {
			seen[r.CreatedByID] = true
			idList = append(idList, r.CreatedByID)
		}
	}
	names := make(map[uint]string, len(idList))
	if len(idList) == 0 {
		return names
	}
	var users []models.User
	h.DB.Where("id IN ?", idList).Find(&users)
	for _, u := range users {
		names[u.ID] = u.FirstName + " " + u.LastName
	}
	return names
}

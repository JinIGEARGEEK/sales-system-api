package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type CampaignHandler struct {
	DB *gorm.DB
}

func NewCampaignHandler(db *gorm.DB) *CampaignHandler {
	return &CampaignHandler{DB: db}
}

// List — GET /campaigns. Newest first.
func (h *CampaignHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Campaign{})

	var total int64
	query.Count(&total)

	var campaigns []models.Campaign
	if err := query.Order("created_at DESC").Limit(perPage).Offset(offset).Find(&campaigns).Error; err != nil {
		return utils.Internal(c, "Failed to list campaigns")
	}
	return utils.List(c, campaigns, page, perPage, total)
}

type campaignForm struct {
	Name string              `json:"name"`
	Type models.CampaignType `json:"type"`
}

// Create — POST /campaigns.
func (h *CampaignHandler) Create(c *fiber.Ctx) error {
	var form campaignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if !models.IsValidCampaignType(form.Type) {
		return utils.ValidationError(c, "type is invalid", map[string][]string{"type": {"invalid"}})
	}

	actorID := middleware.CurrentUserID(c)
	campaign := models.Campaign{Name: form.Name, Type: form.Type, CreatedBy: &actorID}
	if err := h.DB.Create(&campaign).Error; err != nil {
		return utils.Internal(c, "Failed to create campaign")
	}
	return utils.Created(c, campaign)
}

type campaignBulkCreateTasksForm struct {
	CompanyIDs  []uint              `json:"company_ids"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	DueDate     time.Time           `json:"due_date"`
	Priority    models.TaskPriority `json:"priority"`
	AssignedTo  *uint               `json:"assigned_to"`
}

// BulkCreateTasks — POST /campaigns/:id/tasks. Creates one company-related
// Task per company_id, tagged with this campaign, in a single transaction —
// unlike utils.BulkUpdate (which loads and mutates existing rows), this
// creates new rows, so it's a plain transaction loop instead. Writes one
// summary audit-log entry for the whole batch rather than one per task,
// since these tasks don't exist yet for a per-row before/after diff.
func (h *CampaignHandler) BulkCreateTasks(c *fiber.Ctx) error {
	var campaign models.Campaign
	if err := h.DB.First(&campaign, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Campaign not found")
	}

	var form campaignBulkCreateTasksForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.CompanyIDs) == 0 {
		return utils.ValidationError(c, "company_ids is required", map[string][]string{"company_ids": {"required"}})
	}
	// Dedup so a caller-supplied duplicate id can't create two identical
	// tasks for the same company, reusing the same rule utils.BulkUpdate
	// applies to its ids.
	form.CompanyIDs = utils.DedupeUints(form.CompanyIDs)
	if form.Title == "" {
		return utils.ValidationError(c, "title is required", map[string][]string{"title": {"required"}})
	}
	if !models.IsValidTaskPriority(form.Priority) {
		return utils.ValidationError(c, "priority is invalid", map[string][]string{"priority": {"invalid"}})
	}
	// Same ownership rule Task.Create/BulkReassign apply to the incoming
	// assignee — checked once up front since it's identical for every task
	// this creates.
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a task to another sales rep")
	}

	priority := form.Priority
	if priority == "" {
		priority = models.TaskPriorityMedium
	}

	actorID := middleware.CurrentUserID(c)
	tasks := make([]models.Task, 0, len(form.CompanyIDs))
	for _, companyID := range form.CompanyIDs {
		tasks = append(tasks, models.Task{
			RelatedType: models.RelatedTypeCompany,
			RelatedID:   companyID,
			Title:       form.Title,
			Description: form.Description,
			DueDate:     form.DueDate,
			Status:      models.TaskStatusPending,
			Priority:    priority,
			AssignedTo:  form.AssignedTo,
			CampaignID:  &campaign.ID,
		})
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		// Single batch insert (GORM batches a slice Create into one/few
		// statements) instead of one round trip per company — matters once
		// a win-back push targets hundreds of dormant companies.
		if err := tx.Create(&tasks).Error; err != nil {
			return err
		}
		after := models.JSONMap{"campaign_id": campaign.ID, "task_count": len(tasks)}
		return utils.WriteAuditLog(tx, "campaign", campaign.ID, "bulk_created_campaign_tasks", nil, after, actorID)
	})
	if err != nil {
		return utils.Internal(c, "Failed to bulk create campaign tasks")
	}
	return utils.NoContent(c)
}

// Progress — GET /campaigns/:id/progress. total/done/pending count this
// campaign's Tasks; converted counts distinct companies among those tasks'
// related_ids that have since won a Deal (created at or after the campaign
// started) — same EXISTS-subquery shape as applyCompanyFilters' has_won_deal,
// adapted with the campaign-start date condition.
func (h *CampaignHandler) Progress(c *fiber.Ctx) error {
	var campaign models.Campaign
	if err := h.DB.First(&campaign, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Campaign not found")
	}

	var total, done int64
	tasksQuery := h.DB.Model(&models.Task{}).Where("campaign_id = ?", campaign.ID)
	tasksQuery.Count(&total)
	tasksQuery.Session(&gorm.Session{}).Where("status = ?", models.TaskStatusDone).Count(&done)
	// TaskStatus only ever has two values (pending/done), so pending is
	// always total-done — no need for a third Count() round trip.
	pending := total - done

	var converted int64
	if err := h.DB.Model(&models.Task{}).
		Where("campaign_id = ?", campaign.ID).
		Where("EXISTS (SELECT 1 FROM deals WHERE deals.company_id = tasks.related_id AND deals.status = ? AND deals.created_at >= ?)",
			models.DealStatusWon, campaign.CreatedAt).
		Distinct("related_id").
		Count(&converted).Error; err != nil {
		return utils.Internal(c, "Failed to compute campaign progress")
	}

	return utils.OK(c, fiber.Map{
		"total":     total,
		"done":      done,
		"pending":   pending,
		"converted": converted,
	})
}

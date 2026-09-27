package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// NotificationRuleHandler — Admin CRUD for the configurable workflow
// notification rule list (FR-CRM-100/101/102). Mirrors PipelineStageHandler/
// LeadScoringCriteriaHandler: List/Create/Update/Delete, Delete being a soft
// "is_active: false" flip.
type NotificationRuleHandler struct {
	DB *gorm.DB
}

func NewNotificationRuleHandler(db *gorm.DB) *NotificationRuleHandler {
	return &NotificationRuleHandler{DB: db}
}

// List — GET /admin/notification-rules. Always returns every row (active +
// inactive) — the admin config page needs to manage both.
func (h *NotificationRuleHandler) List(c *fiber.Ctx) error {
	var rules []models.NotificationRule
	if err := h.DB.Order("id ASC").Find(&rules).Error; err != nil {
		return utils.Internal(c, "Failed to list notification rules")
	}
	return utils.OK(c, rules)
}

type notificationRuleForm struct {
	Name          string                           `json:"name"`
	EntityType    models.NotificationEntityType    `json:"entity_type"`
	ThresholdDays int                              `json:"threshold_days"`
	RecipientRole models.NotificationRecipientRole `json:"recipient_role"`
	IsActive      *bool                            `json:"is_active"`
	// CreateTask — optional; defaults to true on Create, unchanged on Update
	// when omitted.
	CreateTask *bool `json:"create_task"`
}

func validateNotificationRuleForm(c *fiber.Ctx, form notificationRuleForm) bool {
	if form.Name == "" {
		_ = utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
		return false
	}
	if !models.IsValidNotificationEntityType(form.EntityType) {
		_ = utils.ValidationError(c, "entity_type is invalid", map[string][]string{"entity_type": {"invalid"}})
		return false
	}
	if !models.IsValidNotificationRecipientRole(form.RecipientRole) {
		_ = utils.ValidationError(c, "recipient_role is invalid", map[string][]string{"recipient_role": {"invalid"}})
		return false
	}
	if form.ThresholdDays <= 0 {
		_ = utils.ValidationError(c, "threshold_days must be greater than 0", map[string][]string{"threshold_days": {"must be > 0"}})
		return false
	}
	return true
}

// Create — POST /admin/notification-rules.
func (h *NotificationRuleHandler) Create(c *fiber.Ctx) error {
	var form notificationRuleForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !validateNotificationRuleForm(c, form) {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	rule := models.NotificationRule{
		Name: form.Name, EntityType: form.EntityType, ThresholdDays: form.ThresholdDays,
		RecipientRole: form.RecipientRole, IsActive: form.IsActive == nil || *form.IsActive,
		CreateTask: form.CreateTask == nil || *form.CreateTask,
	}
	rule.CreatedBy = &actorID
	rule.UpdatedBy = &actorID
	// is_active/create_task are NOT NULL DEFAULT true, and GORM leaves a
	// zero-value (false) defaulted field out of the INSERT — and then reads
	// the column default back into the struct — so an explicit false would
	// silently become true. Remember the intent and write it afterwards.
	isActive, createTask := rule.IsActive, rule.CreateTask
	if err := h.DB.Create(&rule).Error; err != nil {
		return utils.ValidationError(c, "Rule name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	if !isActive || !createTask {
		if err := h.DB.Model(&rule).UpdateColumns(map[string]interface{}{
			"is_active": isActive, "create_task": createTask,
		}).Error; err != nil {
			return utils.Internal(c, "Failed to create notification rule")
		}
		rule.IsActive, rule.CreateTask = isActive, createTask
	}
	return utils.Created(c, rule)
}

// Update — PATCH /admin/notification-rules/:id.
func (h *NotificationRuleHandler) Update(c *fiber.Ctx) error {
	var rule models.NotificationRule
	if err := utils.FindByID(c, h.DB, &rule, "Notification rule not found"); err != nil {
		return nil
	}

	var form notificationRuleForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !validateNotificationRuleForm(c, form) {
		return nil
	}

	rule.Name, rule.EntityType, rule.ThresholdDays, rule.RecipientRole = form.Name, form.EntityType, form.ThresholdDays, form.RecipientRole
	if form.IsActive != nil {
		rule.IsActive = *form.IsActive
	}
	if form.CreateTask != nil {
		rule.CreateTask = *form.CreateTask
	}
	actorID := middleware.CurrentUserID(c)
	rule.UpdatedBy = &actorID

	if err := h.DB.Save(&rule).Error; err != nil {
		return utils.Internal(c, "Failed to update notification rule")
	}
	return utils.OK(c, rule)
}

// Delete — DELETE /admin/notification-rules/:id. Soft-delete (is_active:
// false) rather than a hard row delete, same convention as PipelineStage.
func (h *NotificationRuleHandler) Delete(c *fiber.Ctx) error {
	var rule models.NotificationRule
	if err := utils.FindByID(c, h.DB, &rule, "Notification rule not found"); err != nil {
		return nil
	}
	rule.IsActive = false
	actorID := middleware.CurrentUserID(c)
	rule.DeletedBy = &actorID
	if err := h.DB.Save(&rule).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate notification rule")
	}
	return utils.NoContent(c)
}

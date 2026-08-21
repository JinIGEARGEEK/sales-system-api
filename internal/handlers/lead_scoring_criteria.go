package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// LeadScoringCriteriaHandler — Admin CRUD for the configurable lead-scoring
// rule list (FR-CRM-006). Mirrors PipelineStageHandler/LeadSourceHandler:
// List/Create/Update/Delete, Delete being a soft "is_active: false" flip.
type LeadScoringCriteriaHandler struct {
	DB *gorm.DB
}

func NewLeadScoringCriteriaHandler(db *gorm.DB) *LeadScoringCriteriaHandler {
	return &LeadScoringCriteriaHandler{DB: db}
}

// List — GET /admin/lead-scoring-criteria. Always returns every row (active
// + inactive) — the admin config page needs to manage both.
func (h *LeadScoringCriteriaHandler) List(c *fiber.Ctx) error {
	var criteria []models.LeadScoringCriterion
	if err := h.DB.Order("id ASC").Find(&criteria).Error; err != nil {
		return utils.Internal(c, "Failed to list lead scoring criteria")
	}
	return utils.OK(c, criteria)
}

type leadScoringCriterionForm struct {
	Name       string `json:"name"`
	Field      string `json:"field"`
	MatchValue string `json:"match_value"`
	Weight     int    `json:"weight"`
	IsActive   *bool  `json:"is_active"`
}

// Create — POST /admin/lead-scoring-criteria.
func (h *LeadScoringCriteriaHandler) Create(c *fiber.Ctx) error {
	var form leadScoringCriterionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if form.Field == "" {
		return utils.ValidationError(c, "field is required", map[string][]string{"field": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	criterion := models.LeadScoringCriterion{
		Name: form.Name, Field: form.Field, MatchValue: form.MatchValue, Weight: form.Weight,
		IsActive: form.IsActive == nil || *form.IsActive,
	}
	criterion.CreatedBy = &actorID
	criterion.UpdatedBy = &actorID
	if err := h.DB.Create(&criterion).Error; err != nil {
		return utils.ValidationError(c, "Criterion name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, criterion)
}

// Update — PATCH /admin/lead-scoring-criteria/:id.
func (h *LeadScoringCriteriaHandler) Update(c *fiber.Ctx) error {
	var criterion models.LeadScoringCriterion
	if err := h.DB.First(&criterion, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead scoring criterion not found")
	}

	var form leadScoringCriterionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if form.Field == "" {
		return utils.ValidationError(c, "field is required", map[string][]string{"field": {"required"}})
	}

	criterion.Name, criterion.Field, criterion.MatchValue, criterion.Weight = form.Name, form.Field, form.MatchValue, form.Weight
	if form.IsActive != nil {
		criterion.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	criterion.UpdatedBy = &actorID

	if err := h.DB.Save(&criterion).Error; err != nil {
		return utils.Internal(c, "Failed to update lead scoring criterion")
	}
	return utils.OK(c, criterion)
}

// Delete — DELETE /admin/lead-scoring-criteria/:id. Soft-delete (is_active:
// false) rather than a hard row delete, same convention as PipelineStage.
func (h *LeadScoringCriteriaHandler) Delete(c *fiber.Ctx) error {
	var criterion models.LeadScoringCriterion
	if err := h.DB.First(&criterion, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead scoring criterion not found")
	}
	criterion.IsActive = false
	actorID := middleware.CurrentUserID(c)
	criterion.DeletedBy = &actorID
	if err := h.DB.Save(&criterion).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate lead scoring criterion")
	}
	return utils.NoContent(c)
}

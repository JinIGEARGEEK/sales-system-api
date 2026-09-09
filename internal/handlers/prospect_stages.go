package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// ProspectStageHandler — Admin CRUD for the configurable Prospect funnel
// stage list (see ProspectStage's own doc for why "Converted" is reserved
// and excluded). Mirrors PipelineStageHandler's shape: List/Create/Update/
// Delete, Delete being a soft "is_active: false" flip rather than a hard row
// delete (existing Prospects may reference the name).
type ProspectStageHandler struct {
	DB *gorm.DB
}

func NewProspectStageHandler(db *gorm.DB) *ProspectStageHandler {
	return &ProspectStageHandler{DB: db}
}

// List — GET /admin/prospect-stages. Always returns every row (active +
// inactive) ordered by sort_order — the admin config page needs to manage both.
func (h *ProspectStageHandler) List(c *fiber.Ctx) error {
	var stages []models.ProspectStage
	if err := h.DB.Order("sort_order ASC, id ASC").Find(&stages).Error; err != nil {
		return utils.Internal(c, "Failed to list prospect stages")
	}
	return utils.OK(c, stages)
}

type prospectStageForm struct {
	Name                string `json:"name"`
	SortOrder           int    `json:"sort_order"`
	IsActive            *bool  `json:"is_active"`
	IsDisqualifiedStage bool   `json:"is_disqualified_stage"`
}

// Create — POST /admin/prospect-stages.
func (h *ProspectStageHandler) Create(c *fiber.Ctx) error {
	var form prospectStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if form.Name == string(models.ProspectStatusConverted) {
		return utils.ValidationError(c, "Converted is a reserved, system-set stage", map[string][]string{"name": {"\"Converted\" is reserved"}})
	}

	actorID := middleware.CurrentUserID(c)
	stage := models.ProspectStage{
		Name: form.Name, SortOrder: form.SortOrder,
		IsActive: form.IsActive == nil || *form.IsActive,
		IsDisqualifiedStage: form.IsDisqualifiedStage,
	}
	stage.CreatedBy = &actorID
	stage.UpdatedBy = &actorID
	if err := h.DB.Create(&stage).Error; err != nil {
		return utils.ValidationError(c, "Stage name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, stage)
}

// Update — PATCH /admin/prospect-stages/:id.
func (h *ProspectStageHandler) Update(c *fiber.Ctx) error {
	var stage models.ProspectStage
	if err := h.DB.First(&stage, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect stage not found")
	}

	var form prospectStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if form.Name == string(models.ProspectStatusConverted) {
		return utils.ValidationError(c, "Converted is a reserved, system-set stage", map[string][]string{"name": {"\"Converted\" is reserved"}})
	}

	stage.Name, stage.SortOrder = form.Name, form.SortOrder
	stage.IsDisqualifiedStage = form.IsDisqualifiedStage
	if form.IsActive != nil {
		stage.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	stage.UpdatedBy = &actorID

	if err := h.DB.Save(&stage).Error; err != nil {
		return utils.Internal(c, "Failed to update prospect stage")
	}
	return utils.OK(c, stage)
}

// Delete — DELETE /admin/prospect-stages/:id. Soft-delete (is_active: false)
// rather than a hard row delete — existing Prospects may still reference the name.
func (h *ProspectStageHandler) Delete(c *fiber.Ctx) error {
	var stage models.ProspectStage
	if err := h.DB.First(&stage, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect stage not found")
	}
	stage.IsActive = false
	actorID := middleware.CurrentUserID(c)
	stage.DeletedBy = &actorID
	if err := h.DB.Save(&stage).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate prospect stage")
	}
	return utils.NoContent(c)
}

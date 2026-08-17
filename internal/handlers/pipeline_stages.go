package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// PipelineStageHandler — Admin CRUD for the configurable pipeline stage list
// (replaces the previously hardcoded DealStage enum). Mirrors TagHandler's
// shape: List/Create/Update/Delete, Delete being a soft "is_active: false"
// flip rather than a hard row delete (existing Deals may reference the name).
type PipelineStageHandler struct {
	DB *gorm.DB
}

func NewPipelineStageHandler(db *gorm.DB) *PipelineStageHandler {
	return &PipelineStageHandler{DB: db}
}

// List — GET /admin/pipeline-stages. Always returns every row (active +
// inactive) ordered by sort_order — the admin config page needs to manage both.
func (h *PipelineStageHandler) List(c *fiber.Ctx) error {
	var stages []models.PipelineStage
	if err := h.DB.Order("sort_order ASC, id ASC").Find(&stages).Error; err != nil {
		return utils.Internal(c, "Failed to list pipeline stages")
	}
	return utils.OK(c, stages)
}

type pipelineStageForm struct {
	Name        string `json:"name"`
	SortOrder   int    `json:"sort_order"`
	IsActive    *bool  `json:"is_active"`
	IsWonStage  bool   `json:"is_won_stage"`
	IsLostStage bool   `json:"is_lost_stage"`
}

// Create — POST /admin/pipeline-stages.
func (h *PipelineStageHandler) Create(c *fiber.Ctx) error {
	var form pipelineStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	stage := models.PipelineStage{
		Name: form.Name, SortOrder: form.SortOrder,
		IsActive: form.IsActive == nil || *form.IsActive,
		IsWonStage: form.IsWonStage, IsLostStage: form.IsLostStage,
	}
	stage.CreatedBy = &actorID
	stage.UpdatedBy = &actorID
	if err := h.DB.Create(&stage).Error; err != nil {
		return utils.ValidationError(c, "Stage name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, stage)
}

// Update — PATCH /admin/pipeline-stages/:id.
func (h *PipelineStageHandler) Update(c *fiber.Ctx) error {
	var stage models.PipelineStage
	if err := h.DB.First(&stage, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Pipeline stage not found")
	}

	var form pipelineStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	stage.Name, stage.SortOrder = form.Name, form.SortOrder
	stage.IsWonStage, stage.IsLostStage = form.IsWonStage, form.IsLostStage
	if form.IsActive != nil {
		stage.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	stage.UpdatedBy = &actorID

	if err := h.DB.Save(&stage).Error; err != nil {
		return utils.Internal(c, "Failed to update pipeline stage")
	}
	return utils.OK(c, stage)
}

// Delete — DELETE /admin/pipeline-stages/:id. Soft-delete (is_active: false)
// rather than a hard row delete — existing Deals may still reference the name.
func (h *PipelineStageHandler) Delete(c *fiber.Ctx) error {
	var stage models.PipelineStage
	if err := h.DB.First(&stage, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Pipeline stage not found")
	}
	stage.IsActive = false
	actorID := middleware.CurrentUserID(c)
	stage.DeletedBy = &actorID
	if err := h.DB.Save(&stage).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate pipeline stage")
	}
	return utils.NoContent(c)
}

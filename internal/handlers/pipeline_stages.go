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

// List godoc
// @Summary List pipeline stages
// @Description Returns every configured pipeline stage (active + inactive), ordered by sort_order.
// @Tags admin/pipeline-stages
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.PipelineStage
// @Router /admin/pipeline-stages [get]
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

// validate enforces the one name rule shared by Create and Update: required.
// Unlike ProspectStage, PipelineStage has no reserved system-set name.
func (f pipelineStageForm) validate() (map[string][]string, string) {
	if f.Name == "" {
		return map[string][]string{"name": {"required"}}, "name is required"
	}
	return nil, ""
}

// clearOtherTerminalStages unsets is_won_stage/is_lost_stage on every other
// row whose flag would otherwise collide with this save — at most one stage
// may claim each flag, since checkDealIdleRule/UpdateStage and Kanban/badge
// coloring resolve "the" won/lost stage with a single `.First()` lookup and
// would otherwise pick whichever row Postgres happens to return first.
// excludeID is 0 on Create (no row to exclude yet).
func clearOtherTerminalStages(tx *gorm.DB, form pipelineStageForm, excludeID uint) error {
	if form.IsWonStage {
		if err := tx.Model(&models.PipelineStage{}).
			Where("is_won_stage = ? AND id <> ?", true, excludeID).
			Update("is_won_stage", false).Error; err != nil {
			return err
		}
	}
	if form.IsLostStage {
		if err := tx.Model(&models.PipelineStage{}).
			Where("is_lost_stage = ? AND id <> ?", true, excludeID).
			Update("is_lost_stage", false).Error; err != nil {
			return err
		}
	}
	return nil
}

// Create godoc
// @Summary Create a pipeline stage
// @Description Admin-only. Setting is_won_stage/is_lost_stage clears that flag from every other stage.
// @Tags admin/pipeline-stages
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body pipelineStageForm true "Stage fields"
// @Success 201 {object} models.PipelineStage
// @Failure 400 {object} map[string]interface{}
// @Router /admin/pipeline-stages [post]
func (h *PipelineStageHandler) Create(c *fiber.Ctx) error {
	var form pipelineStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if fields, msg := form.validate(); fields != nil {
		return utils.ValidationError(c, msg, fields)
	}

	actorID := middleware.CurrentUserID(c)
	stage := models.PipelineStage{
		Name: form.Name, SortOrder: form.SortOrder,
		IsActive: form.IsActive == nil || *form.IsActive,
		IsWonStage: form.IsWonStage, IsLostStage: form.IsLostStage,
	}
	stage.CreatedBy = &actorID
	stage.UpdatedBy = &actorID

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := clearOtherTerminalStages(tx, form, 0); err != nil {
			return err
		}
		return tx.Create(&stage).Error
	})
	if err != nil {
		return utils.ValidationError(c, "Stage name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, stage)
}

// Update godoc
// @Summary Update a pipeline stage
// @Description Admin-only. Setting is_won_stage/is_lost_stage clears that flag from every other stage.
// @Tags admin/pipeline-stages
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Pipeline stage ID"
// @Param body body pipelineStageForm true "Stage fields"
// @Success 200 {object} models.PipelineStage
// @Failure 404 {object} map[string]interface{}
// @Router /admin/pipeline-stages/{id} [patch]
func (h *PipelineStageHandler) Update(c *fiber.Ctx) error {
	var stage models.PipelineStage
	if err := h.DB.First(&stage, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Pipeline stage not found")
	}

	var form pipelineStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if fields, msg := form.validate(); fields != nil {
		return utils.ValidationError(c, msg, fields)
	}

	stage.Name, stage.SortOrder = form.Name, form.SortOrder
	stage.IsWonStage, stage.IsLostStage = form.IsWonStage, form.IsLostStage
	if form.IsActive != nil {
		stage.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	stage.UpdatedBy = &actorID

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := clearOtherTerminalStages(tx, form, stage.ID); err != nil {
			return err
		}
		return tx.Save(&stage).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to update pipeline stage")
	}
	return utils.OK(c, stage)
}

// Delete godoc
// @Summary Deactivate a pipeline stage
// @Description Admin-only. Soft-delete (is_active: false) — existing Deals may still reference the name.
// @Tags admin/pipeline-stages
// @Security BearerAuth
// @Param id path int true "Pipeline stage ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/pipeline-stages/{id} [delete]
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

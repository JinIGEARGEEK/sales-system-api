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

// List godoc
// @Summary List Prospect stages
// @Description Returns every configured Prospect funnel stage (active + inactive), ordered by sort_order.
// @Tags admin/prospect-stages
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.ProspectStage
// @Router /admin/prospect-stages [get]
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

// validate enforces the two name rules shared by Create and Update: required,
// and "Converted" is reserved for the system-set terminal status (see
// ProspectStage's own doc) and can never be claimed as a configurable stage.
func (f prospectStageForm) validate() (map[string][]string, string) {
	if f.Name == "" {
		return map[string][]string{"name": {"required"}}, "name is required"
	}
	if f.Name == string(models.ProspectStatusConverted) {
		return map[string][]string{"name": {"\"Converted\" is reserved"}}, "Converted is a reserved, system-set stage"
	}
	return nil, ""
}

// clearOtherDisqualifiedStage unsets is_disqualified_stage on every other row
// when this save claims it — at most one stage may carry the flag, since
// checkProspectStaleRule (internal/notifier/workflow_rules.go) resolves "the"
// disqualified stage with a single `.First()` lookup and would otherwise pick
// whichever row Postgres happens to return first. excludeID is 0 on Create
// (no row to exclude yet). Mirrors PipelineStageHandler's
// clearOtherTerminalStages for is_won_stage/is_lost_stage.
func clearOtherDisqualifiedStage(tx *gorm.DB, excludeID uint) error {
	return tx.Model(&models.ProspectStage{}).
		Where("is_disqualified_stage = ? AND id <> ?", true, excludeID).
		Update("is_disqualified_stage", false).Error
}

// Create godoc
// @Summary Create a Prospect stage
// @Description Admin-only. "Converted" is reserved and cannot be used as a stage name. Setting is_disqualified_stage clears that flag from every other stage.
// @Tags admin/prospect-stages
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body prospectStageForm true "Stage fields"
// @Success 201 {object} models.ProspectStage
// @Failure 400 {object} map[string]interface{}
// @Router /admin/prospect-stages [post]
func (h *ProspectStageHandler) Create(c *fiber.Ctx) error {
	var form prospectStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if fields, msg := form.validate(); fields != nil {
		return utils.ValidationError(c, msg, fields)
	}

	actorID := middleware.CurrentUserID(c)
	stage := models.ProspectStage{
		Name: form.Name, SortOrder: form.SortOrder,
		IsActive:            form.IsActive == nil || *form.IsActive,
		IsDisqualifiedStage: form.IsDisqualifiedStage,
	}
	stage.CreatedBy = &actorID
	stage.UpdatedBy = &actorID

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if stage.IsDisqualifiedStage {
			if err := clearOtherDisqualifiedStage(tx, 0); err != nil {
				return err
			}
		}
		return tx.Create(&stage).Error
	})
	if err != nil {
		return utils.ValidationError(c, "Stage name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, stage)
}

// Update godoc
// @Summary Update a Prospect stage
// @Description Admin-only. "Converted" is reserved and cannot be used as a stage name. Setting is_disqualified_stage clears that flag from every other stage.
// @Tags admin/prospect-stages
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Prospect stage ID"
// @Param body body prospectStageForm true "Stage fields"
// @Success 200 {object} models.ProspectStage
// @Failure 404 {object} map[string]interface{}
// @Router /admin/prospect-stages/{id} [patch]
func (h *ProspectStageHandler) Update(c *fiber.Ctx) error {
	var stage models.ProspectStage
	if err := h.DB.First(&stage, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect stage not found")
	}

	var form prospectStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if fields, msg := form.validate(); fields != nil {
		return utils.ValidationError(c, msg, fields)
	}

	stage.Name, stage.SortOrder = form.Name, form.SortOrder
	stage.IsDisqualifiedStage = form.IsDisqualifiedStage
	if form.IsActive != nil {
		stage.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	stage.UpdatedBy = &actorID

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if stage.IsDisqualifiedStage {
			if err := clearOtherDisqualifiedStage(tx, stage.ID); err != nil {
				return err
			}
		}
		return tx.Save(&stage).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to update prospect stage")
	}
	return utils.OK(c, stage)
}

// Delete godoc
// @Summary Deactivate a Prospect stage
// @Description Admin-only. Soft-delete (is_active: false) — existing Prospects may still reference the name.
// @Tags admin/prospect-stages
// @Security BearerAuth
// @Param id path int true "Prospect stage ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/prospect-stages/{id} [delete]
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

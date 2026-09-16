package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// LeadSourceHandler — Admin CRUD for the configurable lead/deal source list
// (replaces the previously hardcoded LeadSource enum shared by Lead.source
// and Deal.channel). Mirrors TagHandler's shape: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
// CRUD logic itself is OptionHandler (option_crud.go) — this just supplies
// the model type, messages, and Swagger docs.
type LeadSourceHandler struct {
	inner *OptionHandler[models.LeadSourceOption, *models.LeadSourceOption]
}

func NewLeadSourceHandler(db *gorm.DB) *LeadSourceHandler {
	return &LeadSourceHandler{inner: &OptionHandler[models.LeadSourceOption, *models.LeadSourceOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list lead sources",
			NotFound:       "Lead source not found",
			NameConflict:   "Source name already in use",
			UpdateFail:     "Failed to update lead source",
			DeactivateFail: "Failed to deactivate lead source",
		},
	}}
}

// List godoc
// @Summary List lead sources
// @Description Returns every configured lead/deal source (active + inactive), ordered by name.
// @Tags admin/lead-sources
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.LeadSourceOption
// @Router /admin/lead-sources [get]
func (h *LeadSourceHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create godoc
// @Summary Create a lead source
// @Description Admin-only.
// @Tags admin/lead-sources
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body OptionForm true "Lead source fields"
// @Success 201 {object} models.LeadSourceOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/lead-sources [post]
func (h *LeadSourceHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update godoc
// @Summary Update a lead source
// @Description Admin-only.
// @Tags admin/lead-sources
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Lead source ID"
// @Param body body OptionForm true "Lead source fields"
// @Success 200 {object} models.LeadSourceOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/lead-sources/{id} [patch]
func (h *LeadSourceHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete godoc
// @Summary Deactivate a lead source
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/lead-sources
// @Security BearerAuth
// @Param id path int true "Lead source ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/lead-sources/{id} [delete]
func (h *LeadSourceHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

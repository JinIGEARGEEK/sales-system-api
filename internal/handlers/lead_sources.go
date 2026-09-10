package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// LeadSourceHandler — Admin CRUD for the configurable lead/deal source list
// (replaces the previously hardcoded LeadSource enum shared by Lead.source
// and Deal.channel). Mirrors TagHandler's shape: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
type LeadSourceHandler struct {
	DB *gorm.DB
}

func NewLeadSourceHandler(db *gorm.DB) *LeadSourceHandler {
	return &LeadSourceHandler{DB: db}
}

// List godoc
// @Summary List lead sources
// @Description Returns every configured lead/deal source (active + inactive), ordered by name.
// @Tags admin/lead-sources
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.LeadSourceOption
// @Router /admin/lead-sources [get]
func (h *LeadSourceHandler) List(c *fiber.Ctx) error {
	var sources []models.LeadSourceOption
	if err := h.DB.Order("name ASC").Find(&sources).Error; err != nil {
		return utils.Internal(c, "Failed to list lead sources")
	}
	return utils.OK(c, sources)
}

type leadSourceForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create godoc
// @Summary Create a lead source
// @Description Admin-only.
// @Tags admin/lead-sources
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body leadSourceForm true "Lead source fields"
// @Success 201 {object} models.LeadSourceOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/lead-sources [post]
func (h *LeadSourceHandler) Create(c *fiber.Ctx) error {
	var form leadSourceForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	source := models.LeadSourceOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	source.CreatedBy = &actorID
	source.UpdatedBy = &actorID
	if err := h.DB.Create(&source).Error; err != nil {
		return utils.ValidationError(c, "Source name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, source)
}

// Update godoc
// @Summary Update a lead source
// @Description Admin-only.
// @Tags admin/lead-sources
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Lead source ID"
// @Param body body leadSourceForm true "Lead source fields"
// @Success 200 {object} models.LeadSourceOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/lead-sources/{id} [patch]
func (h *LeadSourceHandler) Update(c *fiber.Ctx) error {
	var source models.LeadSourceOption
	if err := h.DB.First(&source, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead source not found")
	}

	var form leadSourceForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	source.Name = form.Name
	if form.IsActive != nil {
		source.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	source.UpdatedBy = &actorID

	if err := h.DB.Save(&source).Error; err != nil {
		return utils.Internal(c, "Failed to update lead source")
	}
	return utils.OK(c, source)
}

// Delete godoc
// @Summary Deactivate a lead source
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/lead-sources
// @Security BearerAuth
// @Param id path int true "Lead source ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/lead-sources/{id} [delete]
func (h *LeadSourceHandler) Delete(c *fiber.Ctx) error {
	var source models.LeadSourceOption
	if err := h.DB.First(&source, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead source not found")
	}
	source.IsActive = false
	actorID := middleware.CurrentUserID(c)
	source.DeletedBy = &actorID
	if err := h.DB.Save(&source).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate lead source")
	}
	return utils.NoContent(c)
}

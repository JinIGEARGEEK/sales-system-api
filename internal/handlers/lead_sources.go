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

// List — GET /admin/lead-sources. Always returns every row (active + inactive).
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

// Create — POST /admin/lead-sources.
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

// Update — PATCH /admin/lead-sources/:id.
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

// Delete — DELETE /admin/lead-sources/:id. Soft-delete (is_active: false).
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

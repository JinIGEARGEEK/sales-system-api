package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// ProspectSourceHandler — Admin CRUD for the configurable Prospect source
// list (Marketing's own funnel-source taxonomy, separate from
// LeadSourceHandler's Lead/Deal list — see ProspectSourceOption's doc).
// Mirrors LeadSourceHandler's shape exactly: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
type ProspectSourceHandler struct {
	DB *gorm.DB
}

func NewProspectSourceHandler(db *gorm.DB) *ProspectSourceHandler {
	return &ProspectSourceHandler{DB: db}
}

// List — GET /admin/prospect-sources. Always returns every row (active + inactive).
func (h *ProspectSourceHandler) List(c *fiber.Ctx) error {
	var sources []models.ProspectSourceOption
	if err := h.DB.Order("name ASC").Find(&sources).Error; err != nil {
		return utils.Internal(c, "Failed to list prospect sources")
	}
	return utils.OK(c, sources)
}

type prospectSourceForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create — POST /admin/prospect-sources.
func (h *ProspectSourceHandler) Create(c *fiber.Ctx) error {
	var form prospectSourceForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	source := models.ProspectSourceOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	source.CreatedBy = &actorID
	source.UpdatedBy = &actorID
	if err := h.DB.Create(&source).Error; err != nil {
		return utils.ValidationError(c, "Source name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, source)
}

// Update — PATCH /admin/prospect-sources/:id.
func (h *ProspectSourceHandler) Update(c *fiber.Ctx) error {
	var source models.ProspectSourceOption
	if err := h.DB.First(&source, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect source not found")
	}

	var form prospectSourceForm
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
		return utils.Internal(c, "Failed to update prospect source")
	}
	return utils.OK(c, source)
}

// Delete — DELETE /admin/prospect-sources/:id. Soft-delete (is_active: false).
func (h *ProspectSourceHandler) Delete(c *fiber.Ctx) error {
	var source models.ProspectSourceOption
	if err := h.DB.First(&source, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect source not found")
	}
	source.IsActive = false
	actorID := middleware.CurrentUserID(c)
	source.DeletedBy = &actorID
	if err := h.DB.Save(&source).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate prospect source")
	}
	return utils.NoContent(c)
}

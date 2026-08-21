package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// IndustryOptionHandler — Admin CRUD for the configurable Company industry
// list (replaces the previously frontend-only hardcoded INDUSTRY_OPTIONS
// constant). Mirrors LeadSourceHandler's shape: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
type IndustryOptionHandler struct {
	DB *gorm.DB
}

func NewIndustryOptionHandler(db *gorm.DB) *IndustryOptionHandler {
	return &IndustryOptionHandler{DB: db}
}

// List — GET /admin/industries. Always returns every row (active + inactive).
func (h *IndustryOptionHandler) List(c *fiber.Ctx) error {
	var industries []models.IndustryOption
	if err := h.DB.Order("name ASC").Find(&industries).Error; err != nil {
		return utils.Internal(c, "Failed to list industries")
	}
	return utils.OK(c, industries)
}

type industryOptionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create — POST /admin/industries.
func (h *IndustryOptionHandler) Create(c *fiber.Ctx) error {
	var form industryOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	industry := models.IndustryOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	industry.CreatedBy = &actorID
	industry.UpdatedBy = &actorID
	if err := h.DB.Create(&industry).Error; err != nil {
		return utils.ValidationError(c, "Industry name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, industry)
}

// Update — PATCH /admin/industries/:id.
func (h *IndustryOptionHandler) Update(c *fiber.Ctx) error {
	var industry models.IndustryOption
	if err := h.DB.First(&industry, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Industry not found")
	}

	var form industryOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	industry.Name = form.Name
	if form.IsActive != nil {
		industry.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	industry.UpdatedBy = &actorID

	if err := h.DB.Save(&industry).Error; err != nil {
		return utils.Internal(c, "Failed to update industry")
	}
	return utils.OK(c, industry)
}

// Delete — DELETE /admin/industries/:id. Soft-delete (is_active: false).
func (h *IndustryOptionHandler) Delete(c *fiber.Ctx) error {
	var industry models.IndustryOption
	if err := h.DB.First(&industry, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Industry not found")
	}
	industry.IsActive = false
	actorID := middleware.CurrentUserID(c)
	industry.DeletedBy = &actorID
	if err := h.DB.Save(&industry).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate industry")
	}
	return utils.NoContent(c)
}

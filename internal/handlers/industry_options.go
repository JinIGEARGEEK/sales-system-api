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

// List godoc
// @Summary List industries
// @Description Returns every configured Company industry (active + inactive), ordered by name.
// @Tags admin/industries
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.IndustryOption
// @Router /admin/industries [get]
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

// Create godoc
// @Summary Create an industry
// @Description Admin-only.
// @Tags admin/industries
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body industryOptionForm true "Industry fields"
// @Success 201 {object} models.IndustryOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/industries [post]
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

// Update godoc
// @Summary Update an industry
// @Description Admin-only.
// @Tags admin/industries
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Industry ID"
// @Param body body industryOptionForm true "Industry fields"
// @Success 200 {object} models.IndustryOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/industries/{id} [patch]
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

// Delete godoc
// @Summary Deactivate an industry
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/industries
// @Security BearerAuth
// @Param id path int true "Industry ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/industries/{id} [delete]
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

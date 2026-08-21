package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// CompanySizeOptionHandler — Admin CRUD for the configurable Company size
// bucket list (Size had no controlled list at all before this). Mirrors
// LeadSourceHandler's shape: List/Create/Update/Delete, Delete being a soft
// "is_active: false" flip rather than a hard row delete.
type CompanySizeOptionHandler struct {
	DB *gorm.DB
}

func NewCompanySizeOptionHandler(db *gorm.DB) *CompanySizeOptionHandler {
	return &CompanySizeOptionHandler{DB: db}
}

// List — GET /admin/company-sizes. Always returns every row (active + inactive).
func (h *CompanySizeOptionHandler) List(c *fiber.Ctx) error {
	var sizes []models.CompanySizeOption
	if err := h.DB.Order("name ASC").Find(&sizes).Error; err != nil {
		return utils.Internal(c, "Failed to list company sizes")
	}
	return utils.OK(c, sizes)
}

type companySizeOptionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create — POST /admin/company-sizes.
func (h *CompanySizeOptionHandler) Create(c *fiber.Ctx) error {
	var form companySizeOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	size := models.CompanySizeOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	size.CreatedBy = &actorID
	size.UpdatedBy = &actorID
	if err := h.DB.Create(&size).Error; err != nil {
		return utils.ValidationError(c, "Size name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, size)
}

// Update — PATCH /admin/company-sizes/:id.
func (h *CompanySizeOptionHandler) Update(c *fiber.Ctx) error {
	var size models.CompanySizeOption
	if err := h.DB.First(&size, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company size not found")
	}

	var form companySizeOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	size.Name = form.Name
	if form.IsActive != nil {
		size.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	size.UpdatedBy = &actorID

	if err := h.DB.Save(&size).Error; err != nil {
		return utils.Internal(c, "Failed to update company size")
	}
	return utils.OK(c, size)
}

// Delete — DELETE /admin/company-sizes/:id. Soft-delete (is_active: false).
func (h *CompanySizeOptionHandler) Delete(c *fiber.Ctx) error {
	var size models.CompanySizeOption
	if err := h.DB.First(&size, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company size not found")
	}
	size.IsActive = false
	actorID := middleware.CurrentUserID(c)
	size.DeletedBy = &actorID
	if err := h.DB.Save(&size).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate company size")
	}
	return utils.NoContent(c)
}

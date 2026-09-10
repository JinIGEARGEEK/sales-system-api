package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// RevenueSizeOptionHandler — Admin CRUD for the configurable Company revenue
// bucket list (RevenueSize had no controlled list at all before this). Mirrors
// LeadSourceHandler's shape: List/Create/Update/Delete, Delete being a soft
// "is_active: false" flip rather than a hard row delete.
type RevenueSizeOptionHandler struct {
	DB *gorm.DB
}

func NewRevenueSizeOptionHandler(db *gorm.DB) *RevenueSizeOptionHandler {
	return &RevenueSizeOptionHandler{DB: db}
}

// List godoc
// @Summary List revenue sizes
// @Description Returns every configured Company revenue bucket (active + inactive), ordered by name.
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.RevenueSizeOption
// @Router /admin/revenue-sizes [get]
func (h *RevenueSizeOptionHandler) List(c *fiber.Ctx) error {
	var sizes []models.RevenueSizeOption
	if err := h.DB.Order("name ASC").Find(&sizes).Error; err != nil {
		return utils.Internal(c, "Failed to list revenue sizes")
	}
	return utils.OK(c, sizes)
}

type revenueSizeOptionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create godoc
// @Summary Create a revenue size
// @Description Admin-only.
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body revenueSizeOptionForm true "Revenue size fields"
// @Success 201 {object} models.RevenueSizeOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/revenue-sizes [post]
func (h *RevenueSizeOptionHandler) Create(c *fiber.Ctx) error {
	var form revenueSizeOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	revenueSize := models.RevenueSizeOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	revenueSize.CreatedBy = &actorID
	revenueSize.UpdatedBy = &actorID
	if err := h.DB.Create(&revenueSize).Error; err != nil {
		return utils.ValidationError(c, "Revenue size name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, revenueSize)
}

// Update godoc
// @Summary Update a revenue size
// @Description Admin-only.
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Revenue size ID"
// @Param body body revenueSizeOptionForm true "Revenue size fields"
// @Success 200 {object} models.RevenueSizeOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/revenue-sizes/{id} [patch]
func (h *RevenueSizeOptionHandler) Update(c *fiber.Ctx) error {
	var revenueSize models.RevenueSizeOption
	if err := h.DB.First(&revenueSize, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Revenue size not found")
	}

	var form revenueSizeOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	revenueSize.Name = form.Name
	if form.IsActive != nil {
		revenueSize.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	revenueSize.UpdatedBy = &actorID

	if err := h.DB.Save(&revenueSize).Error; err != nil {
		return utils.Internal(c, "Failed to update revenue size")
	}
	return utils.OK(c, revenueSize)
}

// Delete godoc
// @Summary Deactivate a revenue size
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Param id path int true "Revenue size ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/revenue-sizes/{id} [delete]
func (h *RevenueSizeOptionHandler) Delete(c *fiber.Ctx) error {
	var revenueSize models.RevenueSizeOption
	if err := h.DB.First(&revenueSize, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Revenue size not found")
	}
	revenueSize.IsActive = false
	actorID := middleware.CurrentUserID(c)
	revenueSize.DeletedBy = &actorID
	if err := h.DB.Save(&revenueSize).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate revenue size")
	}
	return utils.NoContent(c)
}

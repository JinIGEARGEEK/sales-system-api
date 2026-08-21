package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// ProductCategoryOptionHandler — Admin CRUD for the configurable Product
// category list (Product.Category had no controlled list at all before
// this). Mirrors LeadSourceHandler's shape: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
type ProductCategoryOptionHandler struct {
	DB *gorm.DB
}

func NewProductCategoryOptionHandler(db *gorm.DB) *ProductCategoryOptionHandler {
	return &ProductCategoryOptionHandler{DB: db}
}

// List — GET /admin/product-categories. Always returns every row (active + inactive).
func (h *ProductCategoryOptionHandler) List(c *fiber.Ctx) error {
	var categories []models.ProductCategoryOption
	if err := h.DB.Order("name ASC").Find(&categories).Error; err != nil {
		return utils.Internal(c, "Failed to list product categories")
	}
	return utils.OK(c, categories)
}

type productCategoryOptionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create — POST /admin/product-categories.
func (h *ProductCategoryOptionHandler) Create(c *fiber.Ctx) error {
	var form productCategoryOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	category := models.ProductCategoryOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	category.CreatedBy = &actorID
	category.UpdatedBy = &actorID
	if err := h.DB.Create(&category).Error; err != nil {
		return utils.ValidationError(c, "Category name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, category)
}

// Update — PATCH /admin/product-categories/:id.
func (h *ProductCategoryOptionHandler) Update(c *fiber.Ctx) error {
	var category models.ProductCategoryOption
	if err := h.DB.First(&category, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Product category not found")
	}

	var form productCategoryOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	category.Name = form.Name
	if form.IsActive != nil {
		category.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	category.UpdatedBy = &actorID

	if err := h.DB.Save(&category).Error; err != nil {
		return utils.Internal(c, "Failed to update product category")
	}
	return utils.OK(c, category)
}

// Delete — DELETE /admin/product-categories/:id. Soft-delete (is_active: false).
func (h *ProductCategoryOptionHandler) Delete(c *fiber.Ctx) error {
	var category models.ProductCategoryOption
	if err := h.DB.First(&category, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Product category not found")
	}
	category.IsActive = false
	actorID := middleware.CurrentUserID(c)
	category.DeletedBy = &actorID
	if err := h.DB.Save(&category).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate product category")
	}
	return utils.NoContent(c)
}

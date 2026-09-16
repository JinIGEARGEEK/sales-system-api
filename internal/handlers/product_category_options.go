package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// ProductCategoryOptionHandler — Admin CRUD for the configurable Product
// category list (Product.Category had no controlled list at all before
// this). Mirrors LeadSourceHandler's shape: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
// CRUD logic itself is OptionHandler (option_crud.go) — this just supplies
// the model type, messages, and Swagger docs.
type ProductCategoryOptionHandler struct {
	inner *OptionHandler[models.ProductCategoryOption, *models.ProductCategoryOption]
}

func NewProductCategoryOptionHandler(db *gorm.DB) *ProductCategoryOptionHandler {
	return &ProductCategoryOptionHandler{inner: &OptionHandler[models.ProductCategoryOption, *models.ProductCategoryOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list product categories",
			NotFound:       "Product category not found",
			NameConflict:   "Category name already in use",
			UpdateFail:     "Failed to update product category",
			DeactivateFail: "Failed to deactivate product category",
		},
	}}
}

// List godoc
// @Summary List product categories
// @Description Returns every configured Product category (active + inactive), ordered by name.
// @Tags admin/product-categories
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.ProductCategoryOption
// @Router /admin/product-categories [get]
func (h *ProductCategoryOptionHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create godoc
// @Summary Create a product category
// @Description Admin-only.
// @Tags admin/product-categories
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body OptionForm true "Product category fields"
// @Success 201 {object} models.ProductCategoryOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/product-categories [post]
func (h *ProductCategoryOptionHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update godoc
// @Summary Update a product category
// @Description Admin-only.
// @Tags admin/product-categories
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Product category ID"
// @Param body body OptionForm true "Product category fields"
// @Success 200 {object} models.ProductCategoryOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/product-categories/{id} [patch]
func (h *ProductCategoryOptionHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete godoc
// @Summary Deactivate a product category
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/product-categories
// @Security BearerAuth
// @Param id path int true "Product category ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/product-categories/{id} [delete]
func (h *ProductCategoryOptionHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

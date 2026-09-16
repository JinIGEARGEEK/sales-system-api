package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// RevenueSizeOptionHandler — Admin CRUD for the configurable Company revenue
// bucket list (RevenueSize had no controlled list at all before this). Mirrors
// LeadSourceHandler's shape: List/Create/Update/Delete, Delete being a soft
// "is_active: false" flip rather than a hard row delete. CRUD logic itself
// is OptionHandler (option_crud.go) — this just supplies the model type,
// messages, and Swagger docs.
type RevenueSizeOptionHandler struct {
	inner *OptionHandler[models.RevenueSizeOption, *models.RevenueSizeOption]
}

func NewRevenueSizeOptionHandler(db *gorm.DB) *RevenueSizeOptionHandler {
	return &RevenueSizeOptionHandler{inner: &OptionHandler[models.RevenueSizeOption, *models.RevenueSizeOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list revenue sizes",
			NotFound:       "Revenue size not found",
			NameConflict:   "Revenue size name already in use",
			UpdateFail:     "Failed to update revenue size",
			DeactivateFail: "Failed to deactivate revenue size",
		},
	}}
}

// List godoc
// @Summary List revenue sizes
// @Description Returns every configured Company revenue bucket (active + inactive), ordered by name.
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.RevenueSizeOption
// @Router /admin/revenue-sizes [get]
func (h *RevenueSizeOptionHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create godoc
// @Summary Create a revenue size
// @Description Admin-only.
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body OptionForm true "Revenue size fields"
// @Success 201 {object} models.RevenueSizeOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/revenue-sizes [post]
func (h *RevenueSizeOptionHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update godoc
// @Summary Update a revenue size
// @Description Admin-only.
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Revenue size ID"
// @Param body body OptionForm true "Revenue size fields"
// @Success 200 {object} models.RevenueSizeOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/revenue-sizes/{id} [patch]
func (h *RevenueSizeOptionHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete godoc
// @Summary Deactivate a revenue size
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/revenue-sizes
// @Security BearerAuth
// @Param id path int true "Revenue size ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/revenue-sizes/{id} [delete]
func (h *RevenueSizeOptionHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

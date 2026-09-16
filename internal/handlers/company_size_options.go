package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// CompanySizeOptionHandler — Admin CRUD for the configurable Company size
// bucket list (Size had no controlled list at all before this). Mirrors
// LeadSourceHandler's shape: List/Create/Update/Delete, Delete being a soft
// "is_active: false" flip rather than a hard row delete. CRUD logic itself
// is OptionHandler (option_crud.go) — this just supplies the model type,
// messages, and Swagger docs.
type CompanySizeOptionHandler struct {
	inner *OptionHandler[models.CompanySizeOption, *models.CompanySizeOption]
}

func NewCompanySizeOptionHandler(db *gorm.DB) *CompanySizeOptionHandler {
	return &CompanySizeOptionHandler{inner: &OptionHandler[models.CompanySizeOption, *models.CompanySizeOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list company sizes",
			NotFound:       "Company size not found",
			NameConflict:   "Size name already in use",
			UpdateFail:     "Failed to update company size",
			DeactivateFail: "Failed to deactivate company size",
		},
	}}
}

// List godoc
// @Summary List company sizes
// @Description Returns every configured Company size bucket (active + inactive), ordered by name.
// @Tags admin/company-sizes
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.CompanySizeOption
// @Router /admin/company-sizes [get]
func (h *CompanySizeOptionHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create godoc
// @Summary Create a company size
// @Description Admin-only.
// @Tags admin/company-sizes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body OptionForm true "Company size fields"
// @Success 201 {object} models.CompanySizeOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/company-sizes [post]
func (h *CompanySizeOptionHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update godoc
// @Summary Update a company size
// @Description Admin-only.
// @Tags admin/company-sizes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Company size ID"
// @Param body body OptionForm true "Company size fields"
// @Success 200 {object} models.CompanySizeOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/company-sizes/{id} [patch]
func (h *CompanySizeOptionHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete godoc
// @Summary Deactivate a company size
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/company-sizes
// @Security BearerAuth
// @Param id path int true "Company size ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/company-sizes/{id} [delete]
func (h *CompanySizeOptionHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

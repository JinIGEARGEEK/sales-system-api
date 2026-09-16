package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// IndustryOptionHandler — Admin CRUD for the configurable Company industry
// list (replaces the previously frontend-only hardcoded INDUSTRY_OPTIONS
// constant). Mirrors LeadSourceHandler's shape: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
// CRUD logic itself is OptionHandler (option_crud.go) — this just supplies
// the model type, messages, and Swagger docs.
type IndustryOptionHandler struct {
	inner *OptionHandler[models.IndustryOption, *models.IndustryOption]
}

func NewIndustryOptionHandler(db *gorm.DB) *IndustryOptionHandler {
	return &IndustryOptionHandler{inner: &OptionHandler[models.IndustryOption, *models.IndustryOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list industries",
			NotFound:       "Industry not found",
			NameConflict:   "Industry name already in use",
			UpdateFail:     "Failed to update industry",
			DeactivateFail: "Failed to deactivate industry",
		},
	}}
}

// List godoc
// @Summary List industries
// @Description Returns every configured Company industry (active + inactive), ordered by name.
// @Tags admin/industries
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.IndustryOption
// @Router /admin/industries [get]
func (h *IndustryOptionHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create godoc
// @Summary Create an industry
// @Description Admin-only.
// @Tags admin/industries
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body OptionForm true "Industry fields"
// @Success 201 {object} models.IndustryOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/industries [post]
func (h *IndustryOptionHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update godoc
// @Summary Update an industry
// @Description Admin-only.
// @Tags admin/industries
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Industry ID"
// @Param body body OptionForm true "Industry fields"
// @Success 200 {object} models.IndustryOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/industries/{id} [patch]
func (h *IndustryOptionHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete godoc
// @Summary Deactivate an industry
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/industries
// @Security BearerAuth
// @Param id path int true "Industry ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/industries/{id} [delete]
func (h *IndustryOptionHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

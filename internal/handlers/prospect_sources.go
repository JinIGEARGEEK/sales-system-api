package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// ProspectSourceHandler — Admin CRUD for the configurable Prospect source
// list (Marketing's own funnel-source taxonomy, separate from
// LeadSourceHandler's Lead/Deal list — see ProspectSourceOption's doc).
// Mirrors LeadSourceHandler's shape exactly: List/Create/Update/Delete,
// Delete being a soft "is_active: false" flip rather than a hard row delete.
// CRUD logic itself is OptionHandler (option_crud.go) — this just supplies
// the model type and messages.
type ProspectSourceHandler struct {
	inner *OptionHandler[models.ProspectSourceOption, *models.ProspectSourceOption]
}

func NewProspectSourceHandler(db *gorm.DB) *ProspectSourceHandler {
	return &ProspectSourceHandler{inner: &OptionHandler[models.ProspectSourceOption, *models.ProspectSourceOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list prospect sources",
			NotFound:       "Prospect source not found",
			NameConflict:   "Source name already in use",
			UpdateFail:     "Failed to update prospect source",
			DeactivateFail: "Failed to deactivate prospect source",
		},
	}}
}

// List — GET /admin/prospect-sources. Always returns every row (active + inactive).
func (h *ProspectSourceHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create — POST /admin/prospect-sources.
func (h *ProspectSourceHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update — PATCH /admin/prospect-sources/:id.
func (h *ProspectSourceHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete — DELETE /admin/prospect-sources/:id. Soft-delete (is_active: false).
func (h *ProspectSourceHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

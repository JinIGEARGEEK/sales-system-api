package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// OptionRow is what OptionHandler needs from a picklist row model — every
// Admin-configurable "name + is_active" resource (LeadSourceOption,
// IndustryOption, CompanySizeOption, RevenueSizeOption, JobTitleOption,
// ProductCategoryOption) implements this via a few one-line methods on the
// model plus AuditedModel's promoted SetCreatedBy/SetUpdatedBy/SetDeletedBy.
type OptionRow interface {
	GetName() string
	SetName(string)
	GetActive() bool
	SetActive(bool)
	SetCreatedBy(*uint)
	SetUpdatedBy(*uint)
	SetDeletedBy(*uint)
}

// OptionMessages holds the copy that differs across the six option resources
// (e.g. "Failed to list lead sources" vs "Failed to list job titles", and the
// irregular conflict wording — "Source name already in use" vs "Job title
// already in use") — the CRUD logic itself is identical and lives once in
// OptionHandler below.
type OptionMessages struct {
	ListFailed       string // "Failed to list <resources>"
	NotFound         string // "<Resource> not found"
	ConflictMessage  string // "<Resource> name already in use"
	UpdateFailed     string // "Failed to update <resource>"
	DeactivateFailed string // "Failed to deactivate <resource>"
}

// OptionHandler is the shared Admin CRUD (List/Create/Update/Delete) behind
// every Admin-configurable picklist — this exact List/Create/Update/Delete
// shape used to be copy-pasted per resource (see git history: LeadSourceHandler,
// IndustryOptionHandler, CompanySizeOptionHandler, RevenueSizeOptionHandler,
// JobTitleOptionHandler, ProductCategoryOptionHandler). Delete is a soft
// "is_active: false" flip, never a hard row delete.
//
// T is the concrete row model (e.g. models.RevenueSizeOption); PT is *T,
// constrained to OptionRow so generic code can get/set its fields — the
// standard "pointer method set" generics pattern, since T's fields aren't
// otherwise reachable across arbitrary embedders.
type OptionHandler[T any, PT interface {
	*T
	OptionRow
}] struct {
	DB  *gorm.DB
	Msg OptionMessages
}

func NewOptionHandler[T any, PT interface {
	*T
	OptionRow
}](db *gorm.DB, msg OptionMessages) *OptionHandler[T, PT] {
	return &OptionHandler[T, PT]{DB: db, Msg: msg}
}

// List — GET /admin/<resource>s. Always returns every row (active + inactive).
func (h *OptionHandler[T, PT]) List(c *fiber.Ctx) error {
	var rows []T
	if err := h.DB.Order("name ASC").Find(&rows).Error; err != nil {
		return utils.Internal(c, h.Msg.ListFailed)
	}
	return utils.OK(c, rows)
}

type optionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create — POST /admin/<resource>s.
func (h *OptionHandler[T, PT]) Create(c *fiber.Ctx) error {
	var form optionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	var row T
	p := PT(&row)
	p.SetName(form.Name)
	p.SetActive(form.IsActive == nil || *form.IsActive)
	p.SetCreatedBy(&actorID)
	p.SetUpdatedBy(&actorID)
	if err := h.DB.Create(&row).Error; err != nil {
		return utils.ValidationError(c, h.Msg.ConflictMessage, map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, row)
}

// Update — PATCH /admin/<resource>s/:id.
func (h *OptionHandler[T, PT]) Update(c *fiber.Ctx) error {
	var row T
	if err := h.DB.First(&row, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, h.Msg.NotFound)
	}

	var form optionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	p := PT(&row)
	p.SetName(form.Name)
	if form.IsActive != nil {
		p.SetActive(*form.IsActive)
	}
	actorID := middleware.CurrentUserID(c)
	p.SetUpdatedBy(&actorID)

	if err := h.DB.Save(&row).Error; err != nil {
		return utils.Internal(c, h.Msg.UpdateFailed)
	}
	return utils.OK(c, row)
}

// Delete — DELETE /admin/<resource>s/:id. Soft-delete (is_active: false).
func (h *OptionHandler[T, PT]) Delete(c *fiber.Ctx) error {
	var row T
	if err := h.DB.First(&row, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, h.Msg.NotFound)
	}
	p := PT(&row)
	p.SetActive(false)
	actorID := middleware.CurrentUserID(c)
	p.SetDeletedBy(&actorID)
	if err := h.DB.Save(&row).Error; err != nil {
		return utils.Internal(c, h.Msg.DeactivateFailed)
	}
	return utils.NoContent(c)
}

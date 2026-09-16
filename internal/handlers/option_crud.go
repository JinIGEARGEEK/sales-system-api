package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// namedOptionModel is satisfied by every Admin-configurable Name/IsActive
// lookup table — CompanySizeOption, JobTitleOption, ProductCategoryOption,
// RevenueSizeOption, LeadSourceOption, IndustryOption, ProspectSourceOption.
// Their List/Create/Update/Delete handlers used to be 100+ line files that
// were byte-for-byte identical apart from the model type and a handful of
// message strings; OptionHandler[T, PT] below is that shared logic, and each
// concrete handler is now a thin wrapper that supplies T, its messages, and
// keeps its own Swagger annotations (generic methods can't carry per-route
// swag docs, so those stay on the thin wrappers, not here).
type namedOptionModel interface {
	GetName() string
	SetName(string)
	GetIsActive() bool
	SetIsActive(bool)
	SetCreatedBy(*uint)
	SetUpdatedBy(*uint)
	SetDeletedBy(*uint)
}

// OptionForm is the shared request body shape for every namedOptionModel
// Create/Update — just Name (required) and an optional IsActive override.
type OptionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// OptionMessages carries the per-type strings each option handler previously
// hardcoded inline (they don't follow a single mechanical template — e.g.
// "Job title already in use" omits "name" where the others include it — so
// this stays explicit per type rather than derived from a label).
type OptionMessages struct {
	ListFail       string
	NotFound       string
	NameConflict   string
	UpdateFail     string
	DeactivateFail string
}

// OptionHandler is List/Create/Update/Delete for any namedOptionModel T (PT
// is *T, carrying the pointer-receiver accessor methods — the standard Go
// generics pattern for "a pointer to T implements this interface").
type OptionHandler[T any, PT interface {
	*T
	namedOptionModel
}] struct {
	DB  *gorm.DB
	Msg OptionMessages
}

func (h *OptionHandler[T, PT]) List(c *fiber.Ctx) error {
	var items []T
	if err := h.DB.Order("name ASC").Find(&items).Error; err != nil {
		return utils.Internal(c, h.Msg.ListFail)
	}
	return utils.OK(c, items)
}

func (h *OptionHandler[T, PT]) Create(c *fiber.Ctx) error {
	var form OptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	var item T
	p := PT(&item)
	p.SetName(form.Name)
	p.SetIsActive(form.IsActive == nil || *form.IsActive)
	p.SetCreatedBy(&actorID)
	p.SetUpdatedBy(&actorID)
	if err := h.DB.Create(&item).Error; err != nil {
		return utils.ValidationError(c, h.Msg.NameConflict, map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, item)
}

func (h *OptionHandler[T, PT]) Update(c *fiber.Ctx) error {
	var item T
	if err := utils.FindByID(c, h.DB, &item, h.Msg.NotFound); err != nil {
		return nil
	}

	var form OptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	p := PT(&item)
	p.SetName(form.Name)
	if form.IsActive != nil {
		p.SetIsActive(*form.IsActive)
	}
	actorID := middleware.CurrentUserID(c)
	p.SetUpdatedBy(&actorID)

	if err := h.DB.Save(&item).Error; err != nil {
		return utils.Internal(c, h.Msg.UpdateFail)
	}
	return utils.OK(c, item)
}

func (h *OptionHandler[T, PT]) Delete(c *fiber.Ctx) error {
	var item T
	if err := utils.FindByID(c, h.DB, &item, h.Msg.NotFound); err != nil {
		return nil
	}
	p := PT(&item)
	p.SetIsActive(false)
	actorID := middleware.CurrentUserID(c)
	p.SetDeletedBy(&actorID)
	if err := h.DB.Save(&item).Error; err != nil {
		return utils.Internal(c, h.Msg.DeactivateFail)
	}
	return utils.NoContent(c)
}

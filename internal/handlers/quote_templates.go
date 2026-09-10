package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// QuoteTemplateHandler — see models.QuoteTemplate's own doc for v1's
// deliberately minimal Save/List/Delete scope (no Update).
type QuoteTemplateHandler struct {
	DB *gorm.DB
}

func NewQuoteTemplateHandler(db *gorm.DB) *QuoteTemplateHandler {
	return &QuoteTemplateHandler{DB: db}
}

// List godoc
// @Summary List quote templates (Admin/Sales Rep/Sales Manager)
// @Description Returns every saved Quote Template, newest first. Not paginated — expected to stay small (a handful of reusable starting points, not a growing archive).
// @Tags quote-templates
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "{data: models.QuoteTemplate[]}"
// @Router /quote-templates [get]
func (h *QuoteTemplateHandler) List(c *fiber.Ctx) error {
	var templates []models.QuoteTemplate
	if err := h.DB.Order("created_at DESC").Find(&templates).Error; err != nil {
		return utils.Internal(c, "Failed to list quote templates")
	}
	return utils.OK(c, templates)
}

type quoteTemplateForm struct {
	Name          string                `json:"name"`
	Items         []models.QuoteItem    `json:"items"`
	ScopeOfWork   string                `json:"scope_of_work"`
	PriceType     models.QuotePriceType `json:"price_type"`
	VatEnabled    bool                  `json:"vat_enabled"`
	WhtEnabled    bool                  `json:"wht_enabled"`
	WhtRate       float64               `json:"wht_rate"`
	DiscountTotal float64               `json:"discount_total"`
	Notes         string                `json:"notes"`
}

// Create godoc
// @Summary Save a quote template (Admin/Sales Rep/Sales Manager)
// @Description Saves a named, deal-independent snapshot of line items/scope/pricing for reuse when creating future Quotes. name is required.
// @Tags quote-templates
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body quoteTemplateForm true "Quote template fields"
// @Success 201 {object} models.QuoteTemplate
// @Failure 400 {object} map[string]interface{} "name is required"
// @Router /quote-templates [post]
func (h *QuoteTemplateHandler) Create(c *fiber.Ctx) error {
	var form quoteTemplateForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	template := models.QuoteTemplate{
		Name:          form.Name,
		Items:         models.JSONItems(snapshotQuoteItems(h.DB, form.Items)),
		ScopeOfWork:   form.ScopeOfWork,
		PriceType:     form.PriceType,
		VatEnabled:    form.VatEnabled,
		WhtEnabled:    form.WhtEnabled,
		WhtRate:       form.WhtRate,
		DiscountTotal: form.DiscountTotal,
		Notes:         form.Notes,
		CreatedBy:     &actorID,
	}
	if template.PriceType == "" {
		template.PriceType = models.QuotePriceTypeExclTax
	}
	if err := h.DB.Create(&template).Error; err != nil {
		return utils.Internal(c, "Failed to save quote template")
	}
	return utils.Created(c, template)
}

// Delete godoc
// @Summary Delete a quote template (Admin/Sales Rep/Sales Manager)
// @Description Hard-deletes a Quote Template — no trash/restore (see models.QuoteTemplate's doc).
// @Tags quote-templates
// @Security BearerAuth
// @Param id path int true "Quote Template ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{} "Quote template not found"
// @Router /quote-templates/{id} [delete]
func (h *QuoteTemplateHandler) Delete(c *fiber.Ctx) error {
	var template models.QuoteTemplate
	if err := h.DB.First(&template, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Quote template not found")
	}
	if err := h.DB.Delete(&template).Error; err != nil {
		return utils.Internal(c, "Failed to delete quote template")
	}
	return utils.NoContent(c)
}

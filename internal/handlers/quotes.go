package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// dealForSubResource loads the parent Deal for a quote/id param and enforces the same
// CanWrite ownership check DealHandler applies directly to the deal itself —
// otherwise a rep blocked from editing a colleague's deal could still edit its quotes.
func dealForSubResource(c *fiber.Ctx, db *gorm.DB, dealIDParam string) (*models.Deal, error) {
	var deal models.Deal
	if err := db.First(&deal, dealIDParam).Error; err != nil {
		return nil, err
	}
	if !CanWrite(c, deal.AssignedTo) {
		return nil, errForbidden
	}
	return &deal, nil
}

type QuoteHandler struct {
	DB *gorm.DB
}

func NewQuoteHandler(db *gorm.DB) *QuoteHandler {
	return &QuoteHandler{DB: db}
}

// List — GET /deals/:dealId/quotes.
func (h *QuoteHandler) List(c *fiber.Ctx) error {
	var quotes []models.Quote
	if err := h.DB.Where("deal_id = ?", c.Params("dealId")).Order("created_at DESC").Find(&quotes).Error; err != nil {
		return utils.Internal(c, "Failed to list quotes")
	}
	return utils.OK(c, quotes)
}

type quoteForm struct {
	Items        []models.QuoteItem `json:"items"`
	ValidityDate *string            `json:"validity_date"`
	Status       models.QuoteStatus `json:"status"`
}

// Create — POST /deals/:dealId/quotes. A line-item quote.
func (h *QuoteHandler) Create(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form quoteForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	quote := models.Quote{
		DealID: deal.ID, Items: models.JSONItems(form.Items),
		ValidityDate: form.ValidityDate, Status: form.Status,
	}
	if quote.Status == "" {
		quote.Status = models.QuoteStatusDraft
	}
	if err := h.DB.Create(&quote).Error; err != nil {
		return utils.Internal(c, "Failed to create quote")
	}
	return utils.Created(c, quote)
}

// Upload — POST /deals/:dealId/quotes/upload. Uploads a PDF quote in place of
// line items — sets file_name/file_url/file_size/uploaded_at, leaves items empty.
func (h *QuoteHandler) Upload(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return utils.BadRequest(c, "Missing file")
	}
	fileURL, size, err := utils.SaveUpload(c, fh)
	if err != nil {
		return utils.RespondUploadError(c, err)
	}

	now := time.Now()
	name := fh.Filename
	quote := models.Quote{
		DealID: deal.ID, Items: models.JSONItems{}, Status: models.QuoteStatusDraft,
		FileName: &name, FileURL: &fileURL, FileSize: &size, UploadedAt: &now,
	}
	if err := h.DB.Create(&quote).Error; err != nil {
		return utils.Internal(c, "Failed to create quote")
	}
	return utils.Created(c, quote)
}

// Update — PUT /quotes/:id. Update status/items/validity_date.
func (h *QuoteHandler) Update(c *fiber.Ctx) error {
	var quote models.Quote
	if err := h.DB.First(&quote, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Quote not found")
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(quote.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form quoteForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	if form.Items != nil {
		quote.Items = models.JSONItems(form.Items)
	}
	if form.ValidityDate != nil {
		quote.ValidityDate = form.ValidityDate
	}
	if form.Status != "" {
		quote.Status = form.Status
	}

	if err := h.DB.Save(&quote).Error; err != nil {
		return utils.Internal(c, "Failed to update quote")
	}
	return utils.OK(c, quote)
}

// Delete — DELETE /quotes/:id (hard delete).
func (h *QuoteHandler) Delete(c *fiber.Ctx) error {
	var quote models.Quote
	if err := h.DB.First(&quote, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Quote not found")
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(quote.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}
	if err := h.DB.Delete(&quote).Error; err != nil {
		return utils.Internal(c, "Failed to delete quote")
	}
	return utils.NoContent(c)
}

// ExportPDF — GET /quotes/:id/export-pdf. Placeholder: real PDF generation is
// out of scope for this first pass.
func (h *QuoteHandler) ExportPDF(c *fiber.Ctx) error {
	return utils.ErrorResponse(c, fiber.StatusNotImplemented, "NOT_IMPLEMENTED", "PDF export not yet implemented")
}

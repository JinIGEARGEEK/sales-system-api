package handlers

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
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
	return utils.OK(c, withEffectiveStatuses(quotes))
}

// withEffectiveStatuses overrides each Quote's Status field with its
// EffectiveStatus() before serialization — so a Sent quote past its
// ValidityDate reports "expired" to callers — without mutating anything in
// the database. Operates on a copy of the slice/values so the caller's
// original in-memory quotes (e.g. ones about to be reused) are unaffected.
func withEffectiveStatuses(quotes []models.Quote) []models.Quote {
	out := make([]models.Quote, len(quotes))
	for i, q := range quotes {
		q.Status = q.EffectiveStatus()
		out[i] = q
	}
	return out
}

// withEffectiveStatus is the single-Quote counterpart of withEffectiveStatuses,
// for Create/Update/Upload responses.
func withEffectiveStatus(q models.Quote) models.Quote {
	q.Status = q.EffectiveStatus()
	return q
}

type quoteForm struct {
	Items        []models.QuoteItem `json:"items"`
	ValidityDate *string            `json:"validity_date"`
	Status       models.QuoteStatus `json:"status"`
}

// snapshotQuoteItems fills Description/Price from the referenced Product for
// any line item that carries a ProductID — a one-time snapshot taken at
// save time, not a live reference. Later edits to the Product's price/name
// never retroactively change a quote that already saved a snapshot. The
// ProductID itself is kept on the item for traceability/reporting. Items
// without a ProductID are left exactly as submitted (pure free text).
func snapshotQuoteItems(db *gorm.DB, items []models.QuoteItem) []models.QuoteItem {
	for i, item := range items {
		if item.ProductID == nil || *item.ProductID == 0 {
			continue
		}
		var product models.Product
		if err := db.First(&product, *item.ProductID).Error; err != nil {
			continue
		}
		items[i].Description = product.Name
		items[i].Price = product.Price
	}
	return items
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
	if form.Status != "" && !models.IsValidQuoteStatus(form.Status) {
		return utils.ValidationError(c, "status is invalid", map[string][]string{
			"status": {"invalid"},
		})
	}

	quote := models.Quote{
		DealID: deal.ID, Items: models.JSONItems(snapshotQuoteItems(h.DB, form.Items)),
		ValidityDate: form.ValidityDate, Status: form.Status,
	}
	if quote.Status == "" {
		quote.Status = models.QuoteStatusDraft
	}
	if err := h.DB.Create(&quote).Error; err != nil {
		return utils.Internal(c, "Failed to create quote")
	}
	return utils.Created(c, withEffectiveStatus(quote))
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
	return utils.Created(c, withEffectiveStatus(quote))
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
	if form.Status != "" && !models.IsValidQuoteStatus(form.Status) {
		return utils.ValidationError(c, "status is invalid", map[string][]string{
			"status": {"invalid"},
		})
	}

	if form.Items != nil {
		quote.Items = models.JSONItems(snapshotQuoteItems(h.DB, form.Items))
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
	return utils.OK(c, withEffectiveStatus(quote))
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

// ExportPDF — GET /quotes/:id/export-pdf. Renders the quote's line items as a
// simple PDF — read-only, same access level as List (no CanWrite check).
func (h *QuoteHandler) ExportPDF(c *fiber.Ctx) error {
	var quote models.Quote
	if err := h.DB.First(&quote, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Quote not found")
	}
	var deal models.Deal
	if err := h.DB.First(&deal, quote.DealID).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	var company models.Company
	h.DB.First(&company, deal.CompanyID)
	var contact models.Contact
	h.DB.First(&contact, deal.ContactID)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, "Quotation")
	pdf.Ln(12)

	pdf.SetFont("Arial", "", 11)
	pdf.Cell(0, 6, fmt.Sprintf("Deal: %s", deal.Title))
	pdf.Ln(6)
	pdf.Cell(0, 6, fmt.Sprintf("Company: %s", company.Name))
	pdf.Ln(6)
	pdf.Cell(0, 6, fmt.Sprintf("Contact: %s", contact.Name))
	pdf.Ln(6)
	if quote.ValidityDate != nil {
		pdf.Cell(0, 6, fmt.Sprintf("Valid Until: %s", *quote.ValidityDate))
		pdf.Ln(6)
	}
	pdf.Cell(0, 6, fmt.Sprintf("Status: %s", quote.EffectiveStatus()))
	pdf.Ln(10)

	utils.RenderLineItemsTable(pdf, quote.Items)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return utils.Internal(c, "Failed to generate PDF")
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="quote-%d.pdf"`, quote.ID))
	return c.Send(buf.Bytes())
}

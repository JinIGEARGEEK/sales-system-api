package handlers

import (
	"bytes"
	"fmt"
	"io"
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
	DB      *gorm.DB
	Storage utils.Storage
}

func NewQuoteHandler(db *gorm.DB, storage utils.Storage) *QuoteHandler {
	return &QuoteHandler{DB: db, Storage: storage}
}

// List godoc
// @Summary List quotes for a deal (Admin/Sales Rep/Sales Manager)
// @Description Returns quotes for a Deal, ordered newest first. Each row's status reflects EffectiveStatus (may report "expired") rather than necessarily the raw stored value. api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Produce json
// @Param dealId path int true "Deal ID"
// @Success 200 {array} models.Quote
// @Router /deals/{dealId}/quotes [get]
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
	ScopeOfWork  string             `json:"scope_of_work"`
	ValidityDate *string            `json:"validity_date"`
	Status       models.QuoteStatus `json:"status"`
	// The rest are all optional/additive (quotation-builder rebuild) — a
	// caller that omits them entirely (any pre-existing client) behaves
	// exactly as before: zero-value CreditDays/WhtRate/DiscountTotal, empty
	// PriceType (defaulted below), VatEnabled/WhtEnabled left at whatever
	// the existing row already has on Update, or their model defaults on
	// Create.
	ReferenceNumber *string               `json:"reference_number"`
	IssueDate       *string               `json:"issue_date"`
	CreditDays      *int                  `json:"credit_days"`
	PriceType       models.QuotePriceType `json:"price_type"`
	VatEnabled      *bool                 `json:"vat_enabled"`
	WhtEnabled      *bool                 `json:"wht_enabled"`
	WhtRate         *float64              `json:"wht_rate"`
	DiscountTotal   *float64              `json:"discount_total"`
	Notes           *string               `json:"notes"`
	InternalNotes   *string               `json:"internal_notes"`
}

// validateQuoteForm runs the checks shared by Create and Update: status enum,
// price_type enum (only when explicitly provided — both are optional-on-PUT
// the same way settingsForm's lead_scoring_mql_threshold is, see settings.go),
// and non-negative CreditDays/WhtRate/DiscountTotal. Writes the 422 response
// itself and returns false on failure, mirroring requireNonNegative's
// convention in settings.go.
func validateQuoteForm(c *fiber.Ctx, form quoteForm) bool {
	if form.Status != "" && !models.IsValidQuoteStatus(form.Status) {
		_ = utils.ValidationError(c, "status is invalid", map[string][]string{"status": {"invalid"}})
		return false
	}
	if form.PriceType != "" && !models.IsValidQuotePriceType(form.PriceType) {
		_ = utils.ValidationError(c, "price_type is invalid", map[string][]string{"price_type": {"invalid"}})
		return false
	}
	if form.CreditDays != nil && *form.CreditDays < 0 {
		_ = utils.ValidationError(c, "credit_days must be non-negative", map[string][]string{"credit_days": {"must be >= 0"}})
		return false
	}
	if form.WhtRate != nil && *form.WhtRate < 0 {
		_ = utils.ValidationError(c, "wht_rate must be non-negative", map[string][]string{"wht_rate": {"must be >= 0"}})
		return false
	}
	if form.DiscountTotal != nil && *form.DiscountTotal < 0 {
		_ = utils.ValidationError(c, "discount_total must be non-negative", map[string][]string{"discount_total": {"must be >= 0"}})
		return false
	}
	return true
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

// Create godoc
// @Summary Create a quote (Admin/Sales Rep/Sales Manager)
// @Description Creates a line-item Quote on a Deal. number is always server-generated, not client-settable. Line items carrying a product_id have their description/price snapshotted from the current Product. Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create. api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body quoteForm true "Quote fields"
// @Success 201 {object} models.Quote
// @Failure 400 {object} map[string]interface{} "Invalid request body, or validation error (status, price_type, credit_days, wht_rate, discount_total)"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{dealId}/quotes [post]
func (h *QuoteHandler) Create(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form quoteForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !validateQuoteForm(c, form) {
		return nil
	}

	quote := models.Quote{
		DealID: deal.ID, Items: models.JSONItems(snapshotQuoteItems(h.DB, form.Items)),
		ScopeOfWork: form.ScopeOfWork, ValidityDate: form.ValidityDate, Status: form.Status,
		ReferenceNumber: form.ReferenceNumber, IssueDate: form.IssueDate,
		PriceType: form.PriceType, Notes: form.Notes, InternalNotes: form.InternalNotes,
	}
	if quote.Status == "" {
		quote.Status = models.QuoteStatusDraft
	}
	if quote.PriceType == "" {
		quote.PriceType = models.QuotePriceTypeExclTax
	}
	if form.CreditDays != nil {
		quote.CreditDays = *form.CreditDays
	}
	if form.VatEnabled != nil {
		quote.VatEnabled = *form.VatEnabled
	} else {
		quote.VatEnabled = true
	}
	if form.WhtEnabled != nil {
		quote.WhtEnabled = *form.WhtEnabled
	}
	if form.WhtRate != nil {
		quote.WhtRate = *form.WhtRate
	}
	if form.DiscountTotal != nil {
		quote.DiscountTotal = *form.DiscountTotal
	}

	// Number generation shares the Create transaction: a failed insert (e.g.
	// a DB constraint error) must roll the sequence increment back too, or a
	// retried create after a failed save would burn numbers.
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		number, err := utils.NextDocumentNumber(tx, "QT", time.Now())
		if err != nil {
			return err
		}
		quote.Number = &number
		return tx.Create(&quote).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to create quote")
	}
	return utils.Created(c, withEffectiveStatus(quote))
}

// Upload — POST /deals/:dealId/quotes/upload. Uploads a PDF quote in place of
// line items — sets file_name/file_url/file_size/uploaded_at. If the PDF
// looks like a FlowAccount quotation export, best-effort extraction
// (utils.ExtractFlowAccountQuote) also pre-fills items/scope_of_work/
// reference_number/issue_date/vat/wht/notes from it — see
// ExtractionStatus/ExtractionWarnings on the response. Extraction is purely
// additive and never fatal: a PDF that isn't a FlowAccount export, or one
// extraction can't make sense of, still uploads exactly as before with
// Items left empty, ExtractionStatus "failed", and no error surfaced.
// Upload godoc
// @Summary Upload a PDF quote (Admin/Sales Rep/Sales Manager)
// @Description Uploads a PDF quote in place of line items — sets file_name/file_url/file_size/uploaded_at. If the PDF looks like a FlowAccount quotation export, best-effort extraction also pre-fills items/scope_of_work/reference_number/issue_date/vat/wht/notes (see extraction_status/extraction_warnings on the response); extraction is never fatal — a PDF that isn't a FlowAccount export still uploads with extraction_status "failed". Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may upload. api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param file formData file true "Quote PDF file"
// @Success 201 {object} models.Quote
// @Failure 400 {object} map[string]interface{} "Missing file, or unsupported file type"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Failure 413 {object} map[string]interface{} "File exceeds 10MB limit"
// @Router /deals/{dealId}/quotes/upload [post]
func (h *QuoteHandler) Upload(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return utils.BadRequest(c, "Missing file")
	}

	// Read the file into memory for extraction before SaveUpload consumes
	// it — best-effort: any failure here (can't open, can't read) just
	// means extraction is skipped, not that the upload itself fails.
	var extraction *utils.FlowAccountExtraction
	if f, openErr := fh.Open(); openErr == nil {
		if data, readErr := io.ReadAll(f); readErr == nil {
			extraction, _ = utils.ExtractFlowAccountQuote(data)
		}
		f.Close()
	}

	key, size, err := h.Storage.Save(fh)
	if err != nil {
		return utils.RespondUploadError(c, err)
	}
	fileURL := "/uploads/" + key

	now := time.Now()
	name := fh.Filename
	quote := models.Quote{
		DealID: deal.ID, Items: models.JSONItems{}, Status: models.QuoteStatusDraft,
		PriceType: models.QuotePriceTypeExclTax, VatEnabled: true,
		FileName: &name, FileURL: &fileURL, FileSize: &size, UploadedAt: &now,
	}
	if extraction != nil {
		status := extraction.Status()
		quote.ExtractionStatus = &status
		quote.ExtractionWarnings = extraction.Warnings
		if extraction.ReferenceNumber != "" {
			quote.ReferenceNumber = &extraction.ReferenceNumber
		}
		if extraction.IssueDate != nil {
			issueDate := extraction.IssueDate.Format("2006-01-02")
			quote.IssueDate = &issueDate
		}
		if extraction.ScopeOfWork != "" {
			quote.ScopeOfWork = extraction.ScopeOfWork
		}
		if extraction.Notes != "" {
			quote.Notes = &extraction.Notes
		}
		quote.VatEnabled = extraction.VatEnabled
		quote.WhtEnabled = extraction.WhtEnabled
		quote.WhtRate = extraction.WhtRate
		if len(extraction.Items) > 0 {
			items := make(models.JSONItems, len(extraction.Items))
			for i, it := range extraction.Items {
				items[i] = models.QuoteItem{Description: it.Description, Qty: it.Qty, Price: it.Price, DiscountPercent: it.DiscountPercent}
			}
			quote.Items = items
		}
	} else {
		failed := "failed"
		quote.ExtractionStatus = &failed
	}
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		number, err := utils.NextDocumentNumber(tx, "QT", now)
		if err != nil {
			return err
		}
		quote.Number = &number
		return tx.Create(&quote).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to create quote")
	}
	return utils.Created(c, withEffectiveStatus(quote))
}

// Update godoc
// @Summary Update a quote
// @Description Updates status/items and every other Quote field (number excepted — immutable after Create). Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may update. api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Quote ID"
// @Param body body quoteForm true "Quote fields"
// @Success 200 {object} models.Quote
// @Failure 400 {object} map[string]interface{} "Invalid request body, or validation error (status, price_type, credit_days, wht_rate, discount_total)"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Quote not found, or deal not found"
// @Router /quotes/{id} [put]
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
	if !validateQuoteForm(c, form) {
		return nil
	}

	if form.Items != nil {
		quote.Items = models.JSONItems(snapshotQuoteItems(h.DB, form.Items))
	}
	// Unconditional, unlike Items/ValidityDate/Status above — a plain string
	// field can't distinguish "omitted" from "explicitly cleared to empty" via
	// BodyParser alone, and the frontend always sends the current value either
	// way (same as Task.Update's Title/Description), so there's no partial-PUT
	// case this would break. Same reasoning applies to ReferenceNumber/
	// IssueDate/Notes/InternalNotes below — pointers, but the frontend always
	// resends them, so unconditional assignment (not "only if non-nil") is
	// correct: it lets a rep explicitly clear one back to empty.
	quote.ScopeOfWork = form.ScopeOfWork
	quote.ReferenceNumber = form.ReferenceNumber
	quote.IssueDate = form.IssueDate
	quote.Notes = form.Notes
	quote.InternalNotes = form.InternalNotes
	if form.ValidityDate != nil {
		quote.ValidityDate = form.ValidityDate
	}
	if form.Status != "" {
		quote.Status = form.Status
	}
	if form.PriceType != "" {
		quote.PriceType = form.PriceType
	}
	if form.CreditDays != nil {
		quote.CreditDays = *form.CreditDays
	}
	if form.VatEnabled != nil {
		quote.VatEnabled = *form.VatEnabled
	}
	if form.WhtEnabled != nil {
		quote.WhtEnabled = *form.WhtEnabled
	}
	if form.WhtRate != nil {
		quote.WhtRate = *form.WhtRate
	}
	if form.DiscountTotal != nil {
		quote.DiscountTotal = *form.DiscountTotal
	}

	if err := h.DB.Save(&quote).Error; err != nil {
		return utils.Internal(c, "Failed to update quote")
	}
	return utils.OK(c, withEffectiveStatus(quote))
}

// Delete godoc
// @Summary Delete a quote
// @Description Hard delete of a Quote. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may delete.
// @Tags quotes
// @Security BearerAuth
// @Param id path int true "Quote ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Quote not found, or deal not found"
// @Router /quotes/{id} [delete]
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

// ExportPDF godoc
// @Summary Export a quote as PDF
// @Description Renders the quote's line items as a PDF — document number, scope of work, line items table (with per-item discount and tax/WHT totals), Deal/Company/Contact header, validity date, status, and notes (never internal_notes). Read-only, same access level as List (no CanWrite ownership check). FR-CRM-042, api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Produce application/pdf
// @Param id path int true "Quote ID"
// @Success 200 {file} file
// @Failure 404 {object} map[string]interface{} "Quote not found, or deal not found"
// @Router /quotes/{id}/export-pdf [get]
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
	if quote.Number != nil {
		pdf.Cell(0, 10, fmt.Sprintf("  %s", *quote.Number))
	}
	pdf.Ln(12)

	pdf.SetFont("Arial", "", 11)
	pdf.Cell(0, 6, fmt.Sprintf("Deal: %s", deal.Title))
	pdf.Ln(6)
	// Same party-info block Contract's export already renders (name/address/
	// tax ID) — previously missing here, closing that gap as part of this
	// rebuild rather than leaving Quote's PDF thinner than Contract's.
	pdf.Cell(0, 6, fmt.Sprintf("Company: %s", strOrDefault(company.LegalName, company.Name)))
	pdf.Ln(6)
	if company.Address != nil && *company.Address != "" {
		pdf.Cell(0, 6, fmt.Sprintf("Address: %s", *company.Address))
		pdf.Ln(6)
	}
	if company.TaxID != nil && *company.TaxID != "" {
		pdf.Cell(0, 6, fmt.Sprintf("Tax ID: %s", *company.TaxID))
		pdf.Ln(6)
	}
	pdf.Cell(0, 6, fmt.Sprintf("Contact: %s", contact.Name))
	pdf.Ln(6)
	if quote.ReferenceNumber != nil && *quote.ReferenceNumber != "" {
		pdf.Cell(0, 6, fmt.Sprintf("Reference No.: %s", *quote.ReferenceNumber))
		pdf.Ln(6)
	}
	if quote.IssueDate != nil {
		pdf.Cell(0, 6, fmt.Sprintf("Date: %s", *quote.IssueDate))
		pdf.Ln(6)
	}
	if quote.CreditDays > 0 {
		pdf.Cell(0, 6, fmt.Sprintf("Credit: %d days", quote.CreditDays))
		pdf.Ln(6)
	}
	if quote.ValidityDate != nil {
		pdf.Cell(0, 6, fmt.Sprintf("Due Date: %s", *quote.ValidityDate))
		pdf.Ln(6)
	}
	priceTypeLabel := "Prices exclude tax"
	if quote.PriceType == models.QuotePriceTypeInclTax {
		priceTypeLabel = "Prices include tax"
	}
	pdf.Cell(0, 6, priceTypeLabel)
	pdf.Ln(6)
	pdf.Cell(0, 6, fmt.Sprintf("Status: %s", quote.EffectiveStatus()))
	pdf.Ln(10)

	if quote.ScopeOfWork != "" {
		pdf.SetFont("Arial", "B", 11)
		pdf.Cell(0, 6, "Scope of Work")
		pdf.Ln(7)
		pdf.SetFont("Arial", "", 10)
		pdf.MultiCell(0, 5, quote.ScopeOfWork, "", "L", false)
		pdf.Ln(4)
	}

	utils.RenderQuoteItemsTable(pdf, quote.Items)

	// Discount total / VAT / WHT / grand total — same formula as
	// utils.ComputeQuoteTotals so this PDF and the edit page's live totals
	// never disagree.
	totals := utils.ComputeQuoteTotals(quote.Items, quote.DiscountTotal, quote.VatEnabled, quote.WhtEnabled, quote.WhtRate)
	pdf.SetFont("Arial", "", 10)
	if quote.DiscountTotal > 0 {
		pdf.Ln(1)
		pdf.CellFormat(165, 7, "Discount", "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("-%.2f", quote.DiscountTotal), "0", 1, "R", false, 0, "")
	}
	if quote.VatEnabled {
		pdf.CellFormat(165, 7, "VAT (7%)", "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("%.2f", totals.Vat), "0", 1, "R", false, 0, "")
	}
	if quote.WhtEnabled {
		pdf.CellFormat(165, 7, fmt.Sprintf("Withholding Tax (%.1f%%)", quote.WhtRate), "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("-%.2f", totals.Wht), "0", 1, "R", false, 0, "")
	}
	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(165, 8, "Grand Total", "0", 0, "R", false, 0, "")
	pdf.CellFormat(30, 8, fmt.Sprintf("%.2f", totals.GrandTotal), "0", 1, "R", false, 0, "")
	pdf.Ln(6)

	// Notes prints; InternalNotes deliberately never reaches this PDF.
	if quote.Notes != nil && *quote.Notes != "" {
		pdf.SetFont("Arial", "B", 10)
		pdf.Cell(0, 6, "Notes")
		pdf.Ln(6)
		pdf.SetFont("Arial", "", 10)
		pdf.MultiCell(0, 5, *quote.Notes, "", "L", false)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return utils.Internal(c, "Failed to generate PDF")
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="quote-%d.pdf"`, quote.ID))
	return c.Send(buf.Bytes())
}

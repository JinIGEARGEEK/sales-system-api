package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/middleware"
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

// quoteWithDeal is a Quote row plus its parent Deal's title, for responses
// read outside a Deal's context (the global search, the full-page editor).
// deal_title is read-only and never written back.
type quoteWithDeal struct {
	models.Quote
	DealTitle string `json:"deal_title"`
}

// Search godoc
// @Summary List / search quotes across all deals (Admin/Sales Rep/Sales Manager/Marketing)
// @Description Paginated quotes, newest first, for the global search bar. search matches number, reference_number or the parent Deal's title (case-insensitive substring). Quotes whose Deal is soft-deleted are excluded. Each row's status reflects EffectiveStatus (may report "expired") and carries deal_title. Row scope matches GET /deals: every sales-pipeline role sees every Deal's quotes. Production: 403.
// @Tags quotes
// @Security BearerAuth
// @Produce json
// @Param search query string false "Match quote number, reference_number or Deal title"
// @Param page query int false "Page (default 1)"
// @Param per_page query int false "Rows per page (default 20, max 200)"
// @Success 200 {object} map[string]interface{} "Paginated quote list (data, page, per_page, total, total_page, next, prev); each row is a Quote plus deal_title"
// @Failure 403 {object} map[string]interface{} "Production role"
// @Router /quotes [get]
func (h *QuoteHandler) Search(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.quotesWithLiveDeal()
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		like := utils.LikePattern(v)
		query = query.Where("(quotes.number ILIKE ? ESCAPE '\\' OR quotes.reference_number ILIKE ? ESCAPE '\\' OR deals.title ILIKE ? ESCAPE '\\')", like, like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return utils.Internal(c, "Failed to list quotes")
	}

	rows := []quoteWithDeal{}
	if err := query.Select(quoteWithDealColumns).
		Order("quotes.created_at DESC, quotes.id DESC").
		Limit(perPage).Offset(offset).Find(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to list quotes")
	}
	for i := range rows {
		rows[i].Quote = withEffectiveStatus(rows[i].Quote)
	}
	return utils.List(c, rows, page, perPage, total)
}

// Get godoc
// @Summary Get a quote (Admin/Sales Rep/Sales Manager/Marketing)
// @Description A single Quote with its effective status (may report "expired") and the parent Deal's deal_title. Read-only, no CanWrite ownership check (same as List/Export-PDF). Backs the full-page Quote editor. Production: 403.
// @Tags quotes
// @Security BearerAuth
// @Produce json
// @Param id path int true "Quote ID"
// @Success 200 {object} quoteWithDeal
// @Failure 403 {object} map[string]interface{} "Production role"
// @Failure 404 {object} map[string]interface{} "Quote not found, or its Deal is deleted"
// @Router /quotes/{id} [get]
func (h *QuoteHandler) Get(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil || id <= 0 {
		return utils.NotFound(c, "Quote not found")
	}
	var row quoteWithDeal
	err = h.quotesWithLiveDeal().Select(quoteWithDealColumns).
		Where("quotes.id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return utils.NotFound(c, "Quote not found")
	}
	if err != nil {
		return utils.Internal(c, "Failed to load quote")
	}
	row.Quote = withEffectiveStatus(row.Quote)
	return utils.OK(c, row)
}

// quoteWithDealColumns is the Select list that fills quoteWithDeal.
const quoteWithDealColumns = "quotes.*, deals.title AS deal_title"

// quotesWithLiveDeal is the Quote query Search and Get share: quotes joined
// to their Deal, leaving out quotes whose Deal is soft-deleted.
func (h *QuoteHandler) quotesWithLiveDeal() *gorm.DB {
	return h.DB.Model(&models.Quote{}).
		Joins("JOIN deals ON deals.id = quotes.deal_id AND deals.deleted_at IS NULL")
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
// non-negative CreditDays/DiscountTotal, WhtRate 0–100 (the spec names no
// fixed list of Thai WHT rates, so any percentage is accepted), then the
// per-item and date checks of quoteFieldErrors in one 422. discount_total's
// upper bound (the subtotal) is validateQuoteDiscount, once the items are
// known. Writes the 422 response itself and returns false on failure,
// mirroring requireNonNegative's convention in settings.go.
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
	if form.WhtRate != nil && (*form.WhtRate < 0 || *form.WhtRate > 100) {
		_ = utils.ValidationError(c, "wht_rate must be between 0 and 100", map[string][]string{"wht_rate": {"must be between 0 and 100"}})
		return false
	}
	if form.DiscountTotal != nil && *form.DiscountTotal < 0 {
		_ = utils.ValidationError(c, "discount_total must be non-negative", map[string][]string{"discount_total": {"must be >= 0"}})
		return false
	}
	if fields := quoteFieldErrors(form); len(fields) > 0 {
		_ = utils.ValidationError(c, "quote has invalid fields", fields)
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
//
// Batches the Product lookup into a single `IN (...)` query over the
// distinct referenced ids rather than one round trip per line item.
func snapshotQuoteItems(db *gorm.DB, items []models.QuoteItem) []models.QuoteItem {
	productIDs := make([]uint, 0, len(items))
	seen := make(map[uint]bool, len(items))
	for _, item := range items {
		if item.ProductID == nil || *item.ProductID == 0 || seen[*item.ProductID] {
			continue
		}
		seen[*item.ProductID] = true
		productIDs = append(productIDs, *item.ProductID)
	}
	if len(productIDs) == 0 {
		return items
	}

	var products []models.Product
	db.Where("id IN ?", productIDs).Find(&products)
	productByID := make(map[uint]models.Product, len(products))
	for _, p := range products {
		productByID[p.ID] = p
	}

	for i, item := range items {
		if item.ProductID == nil || *item.ProductID == 0 {
			continue
		}
		product, ok := productByID[*item.ProductID]
		if !ok {
			continue
		}
		items[i].Description = product.Name
		items[i].Price = product.Price
	}
	return items
}

// Create godoc
// @Summary Create a quote (Admin/Sales Rep/Sales Manager)
// @Description Creates a line-item Quote on a Deal. number is always server-generated, not client-settable. Line items carrying a product_id have their description/price snapshotted from the current Product. Created as accepted with priced items, the Deal's value becomes the quote's pre-VAT taxable amount (rounded to satang) and value_quote_id points at it (deal audit value_synced). Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create. api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body quoteForm true "Quote fields"
// @Success 201 {object} models.Quote
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Failure 409 {object} map[string]interface{} "Created as accepted while the deal already has an Accepted quote"
// @Failure 422 {object} map[string]interface{} "Invalid field (status, price_type, credit_days, wht_rate, discount_total, items[i].qty/price/discount_percent, issue_date, validity_date)"
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
	if !validateQuoteDiscount(c, quote.Items, quote.DiscountTotal) {
		return nil
	}

	// Number generation shares the Create transaction: a failed insert (e.g.
	// a DB constraint error) must roll the sequence increment back too, or a
	// retried create after a failed save would burn numbers. A quote created
	// already Accepted takes the Deal lock, like an accept on Update.
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if quote.Status == models.QuoteStatusAccepted {
			if err := lockRow(tx, &models.Deal{}, deal.ID, "id"); err != nil {
				return err
			}
			if err := ensureSoleAcceptedQuote(tx, deal.ID, 0); err != nil {
				return err
			}
		}
		if err := createQuoteNumbered(tx, &quote, time.Now()); err != nil {
			return err
		}
		return syncDealValueForQuote(tx, &quote, "", middleware.CurrentUserID(c))
	})
	if err != nil {
		return respondLifecycleErr(c, err, "Deal not found", "Failed to create quote")
	}
	return utils.Created(c, withEffectiveStatus(quote))
}

// createQuoteNumbered assigns the next QT number and inserts quote inside
// tx, with utils.CreateKeepingFalse: VatEnabled is NOT NULL DEFAULT true,
// so a plain Create would store vat_enabled false as true.
func createQuoteNumbered(tx *gorm.DB, quote *models.Quote, now time.Time) error {
	number, err := utils.NextDocumentNumber(tx, "QT", now)
	if err != nil {
		return err
	}
	quote.Number = &number
	return utils.CreateKeepingFalse(tx, quote)
}

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
		return createQuoteNumbered(tx, &quote, now)
	})
	if err != nil {
		return utils.Internal(c, "Failed to create quote")
	}
	return utils.Created(c, withEffectiveStatus(quote))
}

// Update godoc
// @Summary Update a quote
// @Description Updates status/items and every other Quote field (number excepted — immutable after Create). Status moves follow models.CanTransitionQuoteStatus (draft→sent/accepted/rejected, sent→draft/accepted/rejected, accepted→rejected; an expired Sent quote can't be accepted), else 409. An Accepted/Rejected quote is read-only: changing any other field is a 409 (resending stored values is not). One Accepted quote per Deal (409 naming the existing one). Accepting a quote with priced items sets the Deal's value to its pre-VAT taxable amount (rounded to satang) and the Deal's value_quote_id to it (deal audit value_synced); moving that quote to rejected clears value_quote_id, keeping the value (value_unsynced). Status changes are audited. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may update. api-system-spec.md §7.4.
// @Tags quotes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Quote ID"
// @Param body body quoteForm true "Quote fields"
// @Success 200 {object} models.Quote
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Quote not found, or deal not found"
// @Failure 409 {object} map[string]interface{} "Status transition not allowed, quote is read-only, the deal already has an Accepted quote, or the quote's status changed meanwhile"
// @Failure 422 {object} map[string]interface{} "Invalid field (status, price_type, credit_days, wht_rate, discount_total, items[i].qty/price/discount_percent, issue_date, validity_date)"
// @Router /quotes/{id} [put]
func (h *QuoteHandler) Update(c *fiber.Ctx) error {
	var quote models.Quote
	if err := utils.FindByID(c, h.DB, &quote, "Quote not found"); err != nil {
		return nil
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(quote.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form quoteForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	// ---- Quote lifecycle guard ----
	// The status move must be in models.CanTransitionQuoteStatus' table
	// (409). An Accepted/Rejected quote is read-only: a body that would
	// change any other field is a 409, and only its status is applied and
	// saved. Content validation below is for editable (draft/sent) quotes.
	oldStatus := quote.Status
	if form.Status != "" && !models.IsValidQuoteStatus(form.Status) {
		return utils.ValidationError(c, "status is invalid", map[string][]string{"status": {"invalid"}})
	}
	newStatus := oldStatus
	if form.Status != "" {
		newStatus = form.Status
	}
	if !checkQuoteTransition(c, quote, newStatus) {
		return nil
	}
	if quote.IsLocked() {
		if field := lockedQuoteChange(c, quote, form); field != "" {
			return utils.Conflict(c, fmt.Sprintf("an %s quote is read-only (%s can't change); duplicate it to revise", quote.Status, field))
		}
		quote.Status = newStatus
		return h.saveQuote(c, &quote, oldStatus)
	}
	// ---- end quote lifecycle guard ----

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
	if !validateQuoteDiscount(c, quote.Items, quote.DiscountTotal) {
		return nil
	}

	return h.saveQuote(c, &quote, oldStatus)
}

// duplicateQuoteDates returns the copy's issue date (today, local) and its
// validity/due date. Create takes both dates from the client verbatim, so
// "recompute the same way" here means keeping the original's credit term:
// the gap between the original's issue and validity dates when both parse,
// else CreditDays when set (it is the credit term behind ValidityDate), else
// no validity date. Dates are written as bare YYYY-MM-DD, a format
// ParseFlexDate already accepts everywhere these fields are read.
func duplicateQuoteDates(src models.Quote, now time.Time) (issue string, validity *string) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	issue = today.Format("2006-01-02")

	days := -1
	if from, ok := models.ParseFlexDate(src.IssueDate); ok {
		if until, ok := models.ParseFlexDate(src.ValidityDate); ok {
			if d := calendar.DaysUntil(from, until); d >= 0 {
				days = d
			}
		}
	}
	if days < 0 && src.CreditDays > 0 {
		days = src.CreditDays
	}
	if days < 0 {
		return issue, nil
	}
	v := today.AddDate(0, 0, days).Format("2006-01-02")
	return issue, &v
}

// Duplicate godoc
// @Summary Duplicate a quote as a new Draft (Admin/Sales Rep/Sales Manager/Marketing)
// @Description Creates a new Draft Quote on the same Deal, copying every editable field (items, scope_of_work, reference_number, credit_days, price_type, vat_enabled, wht_enabled, wht_rate, discount_total, notes, internal_notes). The copy gets a new server-generated number, issue_date = today, and validity_date = today + the original's issue→validity gap (else + credit_days, else null). Never copied: status (always draft), uploaded file fields, extraction_status/warnings. The copy gets revision_of_id = the original chain's root quote and revision_no = the chain's highest + 1; the original is not changed. Same permission as creating a quote on that Deal.
// @Tags quotes
// @Security BearerAuth
// @Produce json
// @Param id path int true "Quote ID to copy"
// @Success 201 {object} models.Quote
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Quote not found, or deal not found"
// @Router /quotes/{id}/duplicate [post]
func (h *QuoteHandler) Duplicate(c *fiber.Ctx) error {
	var src models.Quote
	if err := utils.FindByID(c, h.DB, &src, "Quote not found"); err != nil {
		return nil
	}
	deal, err := dealForSubResource(c, h.DB, fmt.Sprint(src.DealID))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	now := time.Now()
	issue, validity := duplicateQuoteDates(src, now)
	items := make(models.JSONItems, len(src.Items))
	copy(items, src.Items)
	quote := models.Quote{
		DealID: deal.ID, Items: items, ScopeOfWork: src.ScopeOfWork,
		ValidityDate: validity, IssueDate: &issue, Status: models.QuoteStatusDraft,
		ReferenceNumber: src.ReferenceNumber, CreditDays: src.CreditDays, PriceType: src.PriceType,
		VatEnabled: src.VatEnabled, WhtEnabled: src.WhtEnabled, WhtRate: src.WhtRate,
		DiscountTotal: src.DiscountTotal, Notes: src.Notes, InternalNotes: src.InternalNotes,
	}
	if quote.PriceType == "" {
		quote.PriceType = models.QuotePriceTypeExclTax
	}

	// The copy joins src's revision chain: revision_of_id is the chain's
	// root (src itself when src is an original), revision_no the chain's
	// highest + 1. Locking the root row serializes concurrent duplicates of
	// one chain so they can't take the same number. The original is left
	// as it is — rejecting it is the caller's call.
	root := src.ID
	if src.RevisionOfID != nil {
		root = *src.RevisionOfID
	}
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := lockRow(tx, &models.Quote{}, root, "id"); err != nil {
			return err
		}
		var maxNo int
		if err := tx.Model(&models.Quote{}).Where("id = ? OR revision_of_id = ?", root, root).
			Select("COALESCE(MAX(revision_no), 0)").Scan(&maxNo).Error; err != nil {
			return err
		}
		quote.RevisionOfID = &root
		quote.RevisionNo = maxNo + 1
		return createQuoteNumbered(tx, &quote, now)
	})
	if err != nil {
		return utils.Internal(c, "Failed to duplicate quote")
	}
	return utils.Created(c, withEffectiveStatus(quote))
}

// Delete godoc
// @Summary Delete a quote
// @Description Hard delete of a Draft Quote; any other status is a 409. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may delete.
// @Tags quotes
// @Security BearerAuth
// @Param id path int true "Quote ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Quote not found, or deal not found"
// @Failure 409 {object} map[string]interface{} "Quote is not a draft"
// @Router /quotes/{id} [delete]
func (h *QuoteHandler) Delete(c *fiber.Ctx) error {
	var quote models.Quote
	if err := utils.FindByID(c, h.DB, &quote, "Quote not found"); err != nil {
		return nil
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(quote.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}
	// Only a Draft was never in front of the customer. Conditional on the
	// stored status, so a quote sent between the read and here isn't lost.
	if quote.Status != models.QuoteStatusDraft {
		return utils.Conflict(c, fmt.Sprintf("a %s quote can't be deleted; only drafts can", quote.Status))
	}
	res := h.DB.Where("status = ?", models.QuoteStatusDraft).Delete(&quote)
	if res.Error != nil {
		return utils.Internal(c, "Failed to delete quote")
	}
	if res.RowsAffected == 0 {
		return utils.Conflict(c, "only draft quotes can be deleted; this quote has changed, reload it")
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
	if err := utils.FindByID(c, h.DB, &quote, "Quote not found"); err != nil {
		return nil
	}
	var deal models.Deal
	if err := h.DB.First(&deal, quote.DealID).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	var company models.Company
	h.DB.First(&company, deal.CompanyID)
	var contact models.Contact
	h.DB.First(&contact, deal.ContactID)

	pdf := utils.NewPDF()
	pdf.AddPage()

	pdf.SetFont(utils.PDFFont, "B", 16)
	pdf.Cell(0, 10, "Quotation")
	if quote.Number != nil {
		pdf.Cell(0, 10, fmt.Sprintf("  %s", *quote.Number))
	}
	pdf.Ln(12)

	pdf.SetFont(utils.PDFFont, "", 11)
	pdf.Cell(0, 6, fmt.Sprintf("Deal: %s", deal.Title))
	pdf.Ln(6)
	// Same party-info block (name/address/tax ID) as Contract's export.
	utils.RenderPartyBlock(pdf, fmt.Sprintf("Company: %s", utils.StringOrDefault(company.LegalName, company.Name)), company)
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
		pdf.SetFont(utils.PDFFont, "B", 11)
		pdf.Cell(0, 6, "Scope of Work")
		pdf.Ln(7)
		pdf.SetFont(utils.PDFFont, "", 10)
		pdf.MultiCell(0, 5, quote.ScopeOfWork, "", "L", false)
		pdf.Ln(4)
	}

	utils.RenderQuoteItemsTable(pdf, quote.Items)

	// Discount total / VAT / WHT / grand total — same formula as
	// utils.ComputeQuoteTotals so this PDF and the edit page's live totals
	// never disagree.
	totals := utils.QuoteTotalsOf(&quote)
	pdf.SetFont(utils.PDFFont, "", 10)
	if quote.DiscountTotal > 0 {
		pdf.Ln(1)
		pdf.CellFormat(165, 7, "Discount", "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("-%.2f", quote.DiscountTotal), "0", 1, "R", false, 0, "")
	}
	if quote.VatEnabled && quote.PriceType == models.QuotePriceTypeInclTax {
		// Prices already include VAT: show it split out of them, not added.
		pdf.CellFormat(165, 7, "Amount before VAT", "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("%.2f", totals.TaxableAmount), "0", 1, "R", false, 0, "")
		pdf.CellFormat(165, 7, "VAT (7%, included)", "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("%.2f", totals.Vat), "0", 1, "R", false, 0, "")
	} else if quote.VatEnabled {
		pdf.CellFormat(165, 7, "VAT (7%)", "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("%.2f", totals.Vat), "0", 1, "R", false, 0, "")
	}
	if quote.WhtEnabled {
		pdf.CellFormat(165, 7, fmt.Sprintf("Withholding Tax (%.1f%%)", quote.WhtRate), "0", 0, "R", false, 0, "")
		pdf.CellFormat(30, 7, fmt.Sprintf("-%.2f", totals.Wht), "0", 1, "R", false, 0, "")
	}
	pdf.SetFont(utils.PDFFont, "B", 11)
	pdf.CellFormat(165, 8, "Grand Total", "0", 0, "R", false, 0, "")
	pdf.CellFormat(30, 8, fmt.Sprintf("%.2f", totals.GrandTotal), "0", 1, "R", false, 0, "")
	pdf.Ln(6)

	// Notes prints; InternalNotes deliberately never reaches this PDF.
	if quote.Notes != nil && *quote.Notes != "" {
		pdf.SetFont(utils.PDFFont, "B", 10)
		pdf.Cell(0, 6, "Notes")
		pdf.Ln(6)
		pdf.SetFont(utils.PDFFont, "", 10)
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

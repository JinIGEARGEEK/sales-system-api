package models

import (
	"time"

	"github.com/lib/pq"
)

type QuoteStatus string

const (
	QuoteStatusDraft    QuoteStatus = "draft"
	QuoteStatusSent     QuoteStatus = "sent"
	QuoteStatusAccepted QuoteStatus = "accepted"
	QuoteStatusRejected QuoteStatus = "rejected"
	// QuoteStatusExpired is a read-derived state only (see Quote.EffectiveStatus) —
	// it is never a value a caller may set directly via Create/Update, so it is
	// intentionally excluded from ValidQuoteStatuses/IsValidQuoteStatus below.
	QuoteStatusExpired QuoteStatus = "expired"
)

// ValidQuoteStatuses lists every value a caller may directly SET on a Quote's
// Status field via Create/Update — mirrors models.ValidLostReasons's role for
// Deal.LostReason. QuoteStatusExpired is deliberately omitted: it's a derived
// display/filter value computed by EffectiveStatus, never a stored/settable one.
var ValidQuoteStatuses = []QuoteStatus{
	QuoteStatusDraft, QuoteStatusSent, QuoteStatusAccepted, QuoteStatusRejected,
}

func IsValidQuoteStatus(s QuoteStatus) bool {
	for _, v := range ValidQuoteStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// QuotePriceType records whether Quote.Items' Price values are meant to be
// read as tax-exclusive or tax-inclusive — display/PDF concern only, doesn't
// change how ComputeQuoteTotals adds VAT (a quote entered "incl_tax" is
// expected to already have VAT baked into its item prices by whoever typed
// them in; this is a labeling/expectation field, not a second computation
// path).
type QuotePriceType string

const (
	QuotePriceTypeExclTax QuotePriceType = "excl_tax"
	QuotePriceTypeInclTax QuotePriceType = "incl_tax"
)

var ValidQuotePriceTypes = []QuotePriceType{QuotePriceTypeExclTax, QuotePriceTypeInclTax}

func IsValidQuotePriceType(t QuotePriceType) bool {
	for _, v := range ValidQuotePriceTypes {
		if v == t {
			return true
		}
	}
	return false
}

// QuoteItem is stored as a JSON array on Quote.Items (api-system-spec.md §7.4).
// ProductID is optional: when set on incoming create/update requests, the
// handler snapshots that Product's current Name/Price into Description/Price
// at save time — later Product edits never retroactively change a saved
// quote. Left nil/omitted, the item behaves as pure free text, unchanged
// from before this field existed. Being part of a jsonb blob, it's not a
// real FK column — GORM won't enforce referential integrity on it.
type QuoteItem struct {
	Description string  `json:"description"`
	Qty         float64 `json:"qty"`
	Price       float64 `json:"price"`
	ProductID   *uint   `json:"product_id,omitempty"`
	// DiscountPercent (0-100) reduces this item's own line total —
	// independent of Quote.DiscountTotal, which is a further flat-amount
	// discount applied once across the whole quote's subtotal. Defaults to
	// 0 (no discount), so every item created before this field existed
	// behaves exactly as before.
	DiscountPercent float64 `json:"discount_percent,omitempty"`
}

// Quote — api-system-spec.md §7.4.
type Quote struct {
	HardDeleteModel
	DealID uint      `gorm:"not null;index" json:"deal_id"`
	Items  JSONItems `gorm:"type:jsonb" json:"items"`
	// Number is a generated, immutable document number (e.g. "QT2026080004")
	// assigned once at Create time via utils.NextDocumentNumber — never
	// user-edited afterward. See internal/utils/document_number.go. A
	// pointer, not a plain string with "not null": AutoMigrate ADD COLUMN
	// NOT NULL on a table that already has rows would fail outright on any
	// pre-existing database (same class of hazard database.go already
	// avoids for other columns) — every Quote row created going forward
	// always gets one, but rows from before this field existed stay nil
	// rather than requiring a backfill. A unique index tolerates any number
	// of NULLs in Postgres, so this doesn't relax uniqueness for quotes that
	// do have a Number.
	Number *string `gorm:"uniqueIndex" json:"number,omitempty"`
	// ScopeOfWork is a free-text narrative describing the overall engagement
	// (deliverables/phases/terms) — separate from each line item's own
	// Description, which stays a short per-item label. Optional; rendered as
	// a wrapped paragraph above the line-items table in ExportPDF.
	ScopeOfWork  string      `json:"scope_of_work"`
	ValidityDate *string     `json:"validity_date"`
	Status       QuoteStatus `gorm:"type:varchar(16);default:'draft'" json:"status"`
	FileName     *string     `json:"file_name,omitempty"`
	FileURL      *string     `json:"file_url,omitempty"`
	FileSize     *int64      `json:"file_size,omitempty"`
	UploadedAt   *time.Time  `json:"uploaded_at,omitempty"`
	// ReferenceNumber is free-text, user-entered (e.g. the customer's own PO
	// number) — unrelated to Number above, which this system generates.
	ReferenceNumber *string `json:"reference_number,omitempty"`
	// IssueDate is the quote's "as of" date (rendered as "วันที่" in the
	// quotation-builder reference) — parsed the same permissive way as
	// ValidityDate via ParseFlexDate.
	IssueDate *string `json:"issue_date,omitempty"`
	// CreditDays is purely informational context for ValidityDate (which
	// already serves as the actual due date/"ครบกำหนด") — how many days of
	// credit were granted, not itself used in any date arithmetic here.
	CreditDays int            `gorm:"not null;default:0" json:"credit_days"`
	PriceType  QuotePriceType `gorm:"type:varchar(16);default:'excl_tax'" json:"price_type"`
	// VatEnabled toggles Thailand's fixed 7% VAT in ComputeQuoteTotals — no
	// separate rate field, since 7% is the statutory rate, not something a
	// quote should be able to override.
	VatEnabled bool `gorm:"not null;default:true" json:"vat_enabled"`
	// WhtEnabled/WhtRate: withholding tax varies by service type in Thailand
	// (commonly 3% or 5%), so unlike VAT it needs its own rate field. Only
	// meaningful when WhtEnabled is true.
	WhtEnabled bool    `gorm:"not null;default:false" json:"wht_enabled"`
	WhtRate    float64 `gorm:"not null;default:0" json:"wht_rate"`
	// DiscountTotal is a flat currency amount subtracted once from the
	// items' summed (already-per-item-discounted) subtotal — independent of
	// each QuoteItem's own DiscountPercent above.
	DiscountTotal float64 `gorm:"not null;default:0" json:"discount_total"`
	// Notes prints on the exported PDF (payment terms, validity terms,
	// etc.) — InternalNotes deliberately never does; see ExportPDF, which
	// only ever reads Notes.
	Notes         *string `json:"notes,omitempty"`
	InternalNotes *string `json:"internal_notes,omitempty"`
	// ExtractionStatus/ExtractionWarnings record the outcome of best-effort
	// field extraction from an uploaded FlowAccount PDF (Upload handler,
	// utils.ExtractFlowAccountQuote) — nil/empty for every Quote created the
	// normal line-item way, since extraction never runs for those. "ok": every
	// field extraction looked for was found and self-consistent. "partial":
	// some fields extracted, ExtractionWarnings lists what's missing/suspect
	// (e.g. a recomputed total that doesn't match the PDF's printed one) —
	// the rep should double-check those before Sending. "failed": the upload
	// still succeeded and the file is still attached, but the PDF didn't look
	// like a FlowAccount export at all (or had no readable text layer), so
	// nothing was pre-filled. A pointer for the same AutoMigrate-hazard
	// reason as Number above — existing rows never had this column.
	ExtractionStatus   *string        `gorm:"type:varchar(16)" json:"extraction_status,omitempty"`
	ExtractionWarnings pq.StringArray `gorm:"type:text[]" json:"extraction_warnings,omitempty"`
}

func (Quote) TableName() string { return "quotes" }

// ParseFlexDate parses a permissively-formatted date/timestamp value — RFC3339
// (how the frontend serializes a Date) falling back to a bare "2006-01-02" —
// returning ok=false for a malformed or empty value rather than an error, so
// every caller treats "can't parse it" as "skip it" instead of each
// re-implementing the same two-format fallback. Shared by ValidityDate and
// IssueDate (and anything else on Quote that needs the same leniency).
func ParseFlexDate(value *string) (t time.Time, ok bool) {
	if value == nil || *value == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, *value); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", *value); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// ParseValidityDate is ParseFlexDate specialized to ValidityDate — kept as a
// named wrapper since EffectiveStatus/ReportHandler.QuotesExpiringSoon
// already call it by this name; new callers needing the same leniency for a
// different field (e.g. IssueDate) should call ParseFlexDate directly.
func ParseValidityDate(validityDate *string) (t time.Time, ok bool) {
	return ParseFlexDate(validityDate)
}

// EffectiveStatus returns QuoteStatusExpired when this Quote is Sent and its
// ValidityDate has passed, otherwise it returns the stored Status unchanged.
// This is a read-derived value only — it never mutates q.Status or the
// underlying stored row. Only a Sent quote can be considered expired: a
// Draft past its validity date is still a Draft (never sent, nothing to
// expire), and Accepted/Rejected are terminal states that Expired shouldn't
// override.
func (q *Quote) EffectiveStatus() QuoteStatus {
	if q.Status != QuoteStatusSent {
		return q.Status
	}
	validUntil, ok := ParseValidityDate(q.ValidityDate)
	if !ok {
		return q.Status
	}
	if time.Now().After(validUntil) {
		return QuoteStatusExpired
	}
	return q.Status
}

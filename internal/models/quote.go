package models

import "time"

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
}

// Quote — api-system-spec.md §7.4.
type Quote struct {
	HardDeleteModel
	DealID       uint        `gorm:"not null;index" json:"deal_id"`
	Items        JSONItems   `gorm:"type:jsonb" json:"items"`
	ValidityDate *string     `json:"validity_date"`
	Status       QuoteStatus `gorm:"type:varchar(16);default:'draft'" json:"status"`
	FileName     *string     `json:"file_name,omitempty"`
	FileURL      *string     `json:"file_url,omitempty"`
	FileSize     *int64      `json:"file_size,omitempty"`
	UploadedAt   *time.Time  `json:"uploaded_at,omitempty"`
}

func (Quote) TableName() string { return "quotes" }

// ParseValidityDate parses a Quote.ValidityDate value permissively (RFC3339
// timestamp — how the frontend serializes it — falling back to a bare date),
// returning ok=false for a malformed or empty value rather than an error, so
// every caller (EffectiveStatus below, ReportHandler.QuotesExpiringSoon)
// treats "can't parse it" as "skip it" instead of each re-implementing the
// same two-format fallback.
func ParseValidityDate(validityDate *string) (t time.Time, ok bool) {
	if validityDate == nil || *validityDate == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, *validityDate); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", *validityDate); err == nil {
		return t, true
	}
	return time.Time{}, false
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

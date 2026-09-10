package models

import "time"

// QuoteTemplate is a reusable, deal-independent starting point for a new
// Quote — a named snapshot of the fields a rep tends to repeat across
// similar deals (line items, scope of work, pricing/tax settings). Any Sales
// role can save/use/delete one (see routes.go's salesPipelineRoles group).
//
// Deliberately excludes every deal-specific Quote field (validity_date,
// reference_number, status, issue_date, credit_days, internal_notes) — a
// template is meant to be applied to a brand-new Quote, not carry forward
// state that only makes sense for the Quote it was saved from.
//
// Plain hard-deleted (no AuditedModel/soft-delete), mirroring Quote's own
// "hard-deleted server-side" convention — a disposable reusable starting
// point doesn't need trash/restore. No Update endpoint by design (v1 scope):
// delete-and-resave covers the rare case a saved template needs changing.
type QuoteTemplate struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	Name          string         `gorm:"not null" json:"name"`
	Items         JSONItems      `gorm:"type:jsonb" json:"items"`
	ScopeOfWork   string         `json:"scope_of_work"`
	PriceType     QuotePriceType `gorm:"type:varchar(16);default:'excl_tax'" json:"price_type"`
	VatEnabled    bool           `gorm:"not null;default:true" json:"vat_enabled"`
	WhtEnabled    bool           `gorm:"not null;default:false" json:"wht_enabled"`
	WhtRate       float64        `json:"wht_rate"`
	DiscountTotal float64        `json:"discount_total"`
	Notes         string         `json:"notes"`
	CreatedBy     *uint          `json:"created_by,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

func (QuoteTemplate) TableName() string { return "quote_templates" }

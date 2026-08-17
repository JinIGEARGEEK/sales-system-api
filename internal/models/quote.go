package models

import "time"

type QuoteStatus string

const (
	QuoteStatusDraft    QuoteStatus = "draft"
	QuoteStatusSent     QuoteStatus = "sent"
	QuoteStatusAccepted QuoteStatus = "accepted"
	QuoteStatusRejected QuoteStatus = "rejected"
)

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

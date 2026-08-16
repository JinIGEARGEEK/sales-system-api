package models

import "time"

type ContractStatus string

const (
	ContractStatusDraft   ContractStatus = "draft"
	ContractStatusSent    ContractStatus = "sent"
	ContractStatusSigned  ContractStatus = "signed"
	ContractStatusExpired ContractStatus = "expired"
)

// Contract — api-system-spec.md §8.1. QuoteID links back to the Quote a
// Contract's PDF pulls line items/total from (optional — a Contract can be
// drafted before a Quote is finalized).
type Contract struct {
	HardDeleteModel
	DealID        uint           `gorm:"not null;index" json:"deal_id"`
	QuoteID       *uint          `gorm:"index" json:"quote_id"`
	Status        ContractStatus `gorm:"type:varchar(16);default:'draft'" json:"status"`
	SignedFileURL *string        `json:"signed_file_url"`
	SignedDate    *time.Time     `json:"signed_date"`
}

func (Contract) TableName() string { return "contracts" }

package models

import (
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/calendar"
)

type ContractStatus string

const (
	ContractStatusDraft   ContractStatus = "draft"
	ContractStatusSent    ContractStatus = "sent"
	ContractStatusSigned  ContractStatus = "signed"
	ContractStatusExpired ContractStatus = "expired"
)

// ValidContractStatuses/IsValidContractStatus mirror Payment's own
// ValidPaymentMethods/IsValidPaymentMethod (payment.go) — a fixed enum,
// handler-layer validated. PUT /contracts/:id previously accepted any string
// with no check at all (not even enum membership); this closes that gap.
var ValidContractStatuses = []ContractStatus{
	ContractStatusDraft, ContractStatusSent, ContractStatusSigned, ContractStatusExpired,
}

func IsValidContractStatus(s ContractStatus) bool {
	if s == "" {
		return true
	}
	for _, v := range ValidContractStatuses {
		if v == s {
			return true
		}
	}
	return false
}

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
	// EndDate is when the signed contract's term ends (a calendar date).
	// The contract_expiry NotificationRule warns ThresholdDays ahead, once
	// per EndDate value. Optional; nil never fires.
	EndDate *time.Time `gorm:"type:date;index" json:"end_date"`
	// EffectiveStatus is the read-derived status to display: "expired" once a
	// Signed contract's EndDate has passed, else Status. Never stored or
	// accepted on write — Status stays "signed", so the Won gate
	// (validateContractSignedBeforeWon, the frontend's useContractGate) keeps
	// counting it, and a client that saves Status back never turns a
	// lapsed-but-signed contract into a stored "expired" one. Filled by the
	// AfterFind/AfterSave hooks below; see EffectiveStatusAt.
	EffectiveStatus ContractStatus `gorm:"-" json:"effective_status"`
}

func (Contract) TableName() string { return "contracts" }

// EffectiveStatusAt is ContractStatusExpired when the contract is Signed and
// now's server-local calendar day is past EndDate (its last day in force,
// a date column), otherwise the stored Status — mirroring
// Quote.EffectiveStatusAt. A Draft/Sent contract past its end date was never
// in force, so it keeps its own status.
func (c *Contract) EffectiveStatusAt(now time.Time) ContractStatus {
	if c.Status != ContractStatusSigned || c.EndDate == nil {
		return c.Status
	}
	if calendar.Today(now).After(calendar.Day(*c.EndDate)) {
		return ContractStatusExpired
	}
	return c.Status
}

func (c *Contract) fillEffectiveStatus() {
	c.EffectiveStatus = c.EffectiveStatusAt(time.Now())
}

// AfterFind/AfterSave fill EffectiveStatus on every load and every
// Create/Save, so each response that serializes a Contract carries it.
func (c *Contract) AfterFind(*gorm.DB) error {
	c.fillEffectiveStatus()
	return nil
}

func (c *Contract) AfterSave(*gorm.DB) error {
	c.fillEffectiveStatus()
	return nil
}

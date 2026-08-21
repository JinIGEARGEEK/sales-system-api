package models

import "time"

type PaymentMethod string

const (
	PaymentMethodCash     PaymentMethod = "cash"
	PaymentMethodTransfer PaymentMethod = "transfer"
	PaymentMethodCard     PaymentMethod = "card"
	PaymentMethodOther    PaymentMethod = "other"
)

// ValidPaymentMethods lists every accepted PaymentMethod value, for
// handler-layer validation — a fixed accounting-category enum, not an
// open-ended list.
var ValidPaymentMethods = []PaymentMethod{
	PaymentMethodCash, PaymentMethodTransfer, PaymentMethodCard, PaymentMethodOther,
}

func IsValidPaymentMethod(m PaymentMethod) bool {
	if m == "" {
		return true
	}
	for _, v := range ValidPaymentMethods {
		if v == m {
			return true
		}
	}
	return false
}

// Payment — api-system-spec.md §7.5.
type Payment struct {
	HardDeleteModel
	DealID uint          `gorm:"not null;index" json:"deal_id"`
	Amount float64       `json:"amount"`
	PaidAt time.Time     `json:"paid_at"`
	Method PaymentMethod `gorm:"type:varchar(16)" json:"method"`
	Note   string        `json:"note"`
}

func (Payment) TableName() string { return "payments" }

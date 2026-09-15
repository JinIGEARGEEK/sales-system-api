package models

import "time"

// PaymentInstallment — a planned installment on a Deal's payment schedule,
// defined before money actually arrives (unlike Payment, which only records
// money already received — see payment.go). No status is stored here: it's
// always derived by comparing the schedule against actual Payment totals
// (utils.ComputeInstallmentStatuses, a cumulative "waterfall" allocation —
// no explicit link to which Payment satisfies which installment), so
// editing/reordering installments never leaves stale state. api-system-spec.md §7.5a.
type PaymentInstallment struct {
	HardDeleteModel           // matches Payment's own delete semantics — planning data, not audit-critical
	DealID          uint      `gorm:"not null;index" json:"deal_id"`
	Amount          float64   `json:"amount"`
	DueDate         time.Time `json:"due_date"`
	Note            string    `json:"note"`
}

func (PaymentInstallment) TableName() string { return "payment_installments" }

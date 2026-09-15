package utils

import (
	"sort"
	"time"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// InstallmentStatus is one PaymentInstallment plus its derived state —
// "derived" because PaymentInstallment itself stores no status (see that
// model's own doc comment).
type InstallmentStatus struct {
	Installment models.PaymentInstallment `json:"installment"`
	// Covered is how much of this installment's Amount has been paid,
	// cumulatively — see ComputeInstallmentStatuses's own doc for the
	// waterfall allocation this comes from.
	Covered float64 `json:"covered"`
	Status  string  `json:"status"` // "paid" | "partial" | "overdue" | "upcoming"
}

const (
	InstallmentStatusPaid     = "paid"
	InstallmentStatusPartial  = "partial"
	InstallmentStatusOverdue  = "overdue"
	InstallmentStatusUpcoming = "upcoming"
)

// ComputeInstallmentStatuses derives each installment's status from a
// Deal's total actual Payments, via a cumulative "waterfall" allocation —
// confirmed with the business owner as the reconciliation model instead of
// explicitly linking a Payment to a specific installment (which would need
// changes to how Payments are logged today). Installments are sorted by
// DueDate ascending and money is applied to the earliest ones first: an
// installment is "paid" once fully covered, "overdue" if due-dated in the
// past and not fully covered (even partially), "partial" if not fully
// covered but not yet due, "upcoming" if nothing has been applied to it yet
// and it isn't overdue.
func ComputeInstallmentStatuses(installments []models.PaymentInstallment, totalPaid float64, now time.Time) []InstallmentStatus {
	sorted := make([]models.PaymentInstallment, len(installments))
	copy(sorted, installments)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].DueDate.Before(sorted[j].DueDate) })

	remaining := totalPaid
	statuses := make([]InstallmentStatus, 0, len(sorted))
	for _, inst := range sorted {
		covered := inst.Amount
		if remaining < covered {
			covered = remaining
		}
		if covered < 0 {
			covered = 0
		}
		remaining -= covered

		isOverdue := inst.DueDate.Before(now)
		var status string
		switch {
		case covered >= inst.Amount:
			status = InstallmentStatusPaid
		case isOverdue:
			status = InstallmentStatusOverdue
		case covered > 0:
			status = InstallmentStatusPartial
		default:
			status = InstallmentStatusUpcoming
		}

		statuses = append(statuses, InstallmentStatus{Installment: inst, Covered: covered, Status: status})
	}
	return statuses
}

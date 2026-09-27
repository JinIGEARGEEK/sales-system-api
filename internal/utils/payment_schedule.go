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
// confirmed with the business owner as the reconciliation model. Installments
// are sorted by DueDate ascending and money is applied to the earliest ones
// first: an installment is "paid" once fully covered, "overdue" if due-dated
// in the past and not fully covered (even partially), "partial" if not fully
// covered but not yet due, "upcoming" if nothing has been applied to it yet
// and it isn't overdue.
//
// Callers holding the Payment rows themselves should use
// ComputeInstallmentStatusesFromPayments instead, which also honors
// Payment.InstallmentID links and counts withholding tax as settled.
func ComputeInstallmentStatuses(installments []models.PaymentInstallment, totalPaid float64, now time.Time) []InstallmentStatus {
	return allocateInstallments(installments, nil, totalPaid, now)
}

// ComputeInstallmentStatusesFromPayments is ComputeInstallmentStatuses for a
// Deal's actual Payment rows. Each payment settles SettledAmount() (cash +
// WHT deducted). A payment linked to one of these installments
// (Payment.InstallmentID) is credited to that installment first — so paying
// installment #2 early marks #2 paid rather than #1 — and any excess over
// that installment's amount joins the unlinked money, which waterfalls
// across whatever is still uncovered in due-date order exactly as before.
// With no linked payments the result is identical to
// ComputeInstallmentStatuses(installments, Σ SettledAmount, now).
func ComputeInstallmentStatusesFromPayments(installments []models.PaymentInstallment, payments []models.Payment, now time.Time) []InstallmentStatus {
	known := make(map[uint]bool, len(installments))
	for _, inst := range installments {
		known[inst.ID] = true
	}
	linked := map[uint]float64{}
	var pool float64
	for _, p := range payments {
		if p.InstallmentID != nil && known[*p.InstallmentID] {
			linked[*p.InstallmentID] += p.SettledAmount()
			continue
		}
		pool += p.SettledAmount()
	}
	return allocateInstallments(installments, linked, pool, now)
}

// allocateInstallments applies linked credit to each installment, adds any
// linked excess to pool, then waterfalls pool over the remaining gaps in
// due-date order.
func allocateInstallments(installments []models.PaymentInstallment, linked map[uint]float64, pool float64, now time.Time) []InstallmentStatus {
	sorted := make([]models.PaymentInstallment, len(installments))
	copy(sorted, installments)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].DueDate.Before(sorted[j].DueDate) })

	covered := make([]float64, len(sorted))
	for i, inst := range sorted {
		credit := linked[inst.ID]
		if credit <= 0 {
			continue
		}
		covered[i] = credit
		if credit > inst.Amount {
			covered[i] = inst.Amount
			pool += credit - inst.Amount
		}
	}

	statuses := make([]InstallmentStatus, 0, len(sorted))
	for i, inst := range sorted {
		if gap := inst.Amount - covered[i]; gap > 0 && pool > 0 {
			take := gap
			if pool < take {
				take = pool
			}
			covered[i] += take
			pool -= take
		}

		// Overdue from the day after the due date, by calendar day — the
		// same count DaysOverdue/AgingBucket use, so an installment isn't
		// "overdue" on its own due date while its aging says current.
		isOverdue := DaysOverdue(inst.DueDate, now) > 0
		var status string
		switch {
		// Within MoneyEpsilon: cash + WHT from a percentage lands a hair
		// short of the installment (e.g. 9,699.999…), which is still paid.
		case covered[i] >= inst.Amount-MoneyEpsilon:
			status = InstallmentStatusPaid
		case isOverdue:
			status = InstallmentStatusOverdue
		case covered[i] > 0:
			status = InstallmentStatusPartial
		default:
			status = InstallmentStatusUpcoming
		}

		statuses = append(statuses, InstallmentStatus{Installment: inst, Covered: covered[i], Status: status})
	}
	return statuses
}

// MoneyEpsilon absorbs float rounding in baht amounts (7% VAT or 3% WHT on
// odd amounts), so a sum paid to the satang compares as settled rather than
// owing 0.0000001. Shared by the installment allocation and the Outstanding
// Balance report.
const MoneyEpsilon = 0.005

// Receivable aging buckets (Outstanding Balance report), by whole days past
// the oldest unpaid installment's due date.
const (
	AgingCurrent = "current"
	Aging1To30   = "1_30"
	Aging31To60  = "31_60"
	Aging61To90  = "61_90"
	Aging90Plus  = "90_plus"
)

// AgingBucket maps days overdue to its bucket; 0 or less is "current".
func AgingBucket(daysOverdue int) string {
	switch {
	case daysOverdue <= 0:
		return AgingCurrent
	case daysOverdue <= 30:
		return Aging1To30
	case daysOverdue <= 60:
		return Aging31To60
	case daysOverdue <= 90:
		return Aging61To90
	default:
		return Aging90Plus
	}
}

// DaysOverdue counts whole calendar days (server-local) from due to now —
// 0 on the due date itself, negative before it. Calendar days rather than
// elapsed hours, so a due date stored as UTC midnight doesn't flip buckets
// at 07:00 Bangkok time.
func DaysOverdue(due, now time.Time) int {
	due, now = due.In(time.Local), now.In(time.Local)
	d := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	n := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(n.Sub(d).Hours() / 24)
}

// OldestOverdue returns the earliest-due installment that is overdue per
// statuses, or nil when none is.
func OldestOverdue(statuses []InstallmentStatus) *models.PaymentInstallment {
	var oldest *models.PaymentInstallment
	for i := range statuses {
		if statuses[i].Status != InstallmentStatusOverdue {
			continue
		}
		if oldest == nil || statuses[i].Installment.DueDate.Before(oldest.DueDate) {
			oldest = &statuses[i].Installment
		}
	}
	return oldest
}

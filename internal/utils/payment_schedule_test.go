package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/models"
)

func daysFromNow(now time.Time, days int) time.Time { return now.AddDate(0, 0, days) }

// TestComputeInstallmentStatuses_Waterfall reproduces the worked example
// from the feature's own design: 3 installments (30k/40k/30k), 50k paid so
// far -> #1 fully covered (paid), #2 partially covered (partial, not yet
// due) or overdue if its due date has passed, #3 untouched (upcoming).
func TestComputeInstallmentStatuses_Waterfall(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	installments := []models.PaymentInstallment{
		{Amount: 30000, DueDate: daysFromNow(now, -20)},
		{Amount: 40000, DueDate: daysFromNow(now, 10)},
		{Amount: 30000, DueDate: daysFromNow(now, 40)},
	}

	statuses := ComputeInstallmentStatuses(installments, 50000, now)

	assert.Equal(t, InstallmentStatusPaid, statuses[0].Status)
	assert.InDelta(t, 30000.0, statuses[0].Covered, 0.001)

	assert.Equal(t, InstallmentStatusPartial, statuses[1].Status)
	assert.InDelta(t, 20000.0, statuses[1].Covered, 0.001)

	assert.Equal(t, InstallmentStatusUpcoming, statuses[2].Status)
	assert.InDelta(t, 0.0, statuses[2].Covered, 0.001)
}

// TestComputeInstallmentStatuses_Overdue guards the overdue branch: a
// partially- (or entirely-) uncovered installment whose due date has passed
// is "overdue", not "partial"/"upcoming".
func TestComputeInstallmentStatuses_Overdue(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	installments := []models.PaymentInstallment{
		{Amount: 30000, DueDate: daysFromNow(now, -5)},
	}

	statuses := ComputeInstallmentStatuses(installments, 0, now)

	assert.Equal(t, InstallmentStatusOverdue, statuses[0].Status)
}

// TestComputeInstallmentStatuses_SortsByDueDate confirms money is applied to
// the earliest-due installment first regardless of input order.
func TestComputeInstallmentStatuses_SortsByDueDate(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	installments := []models.PaymentInstallment{
		{Amount: 10000, DueDate: daysFromNow(now, 30)},
		{Amount: 10000, DueDate: daysFromNow(now, -10)},
	}

	statuses := ComputeInstallmentStatuses(installments, 10000, now)

	// Earliest due date (was second in the input) is paid first.
	assert.InDelta(t, -10.0, statuses[0].Installment.DueDate.Sub(now).Hours()/24, 0.01)
	assert.Equal(t, InstallmentStatusPaid, statuses[0].Status)
	assert.Equal(t, InstallmentStatusUpcoming, statuses[1].Status)
}

// TestComputeInstallmentStatuses_FullyPaidNotOverdue guards against an
// installment that's fully covered but past its due date being misclassified
// as overdue — "paid" always wins regardless of due date.
func TestComputeInstallmentStatuses_FullyPaidNotOverdue(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	installments := []models.PaymentInstallment{
		{Amount: 10000, DueDate: daysFromNow(now, -30)},
	}

	statuses := ComputeInstallmentStatuses(installments, 10000, now)

	assert.Equal(t, InstallmentStatusPaid, statuses[0].Status)
}

// A payment linked to installment #2 settles #2 first, even though #1 is
// due earlier; without the link the same money would waterfall into #1.
func TestComputeInstallmentStatusesFromPayments_LinkedFirst(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	installments := []models.PaymentInstallment{
		{HardDeleteModel: models.HardDeleteModel{ID: 1}, Amount: 30000, DueDate: daysFromNow(now, -10)},
		{HardDeleteModel: models.HardDeleteModel{ID: 2}, Amount: 40000, DueDate: daysFromNow(now, 20)},
	}
	two := uint(2)
	payments := []models.Payment{{Amount: 40000, InstallmentID: &two}}

	statuses := ComputeInstallmentStatusesFromPayments(installments, payments, now)
	assert.Equal(t, InstallmentStatusOverdue, statuses[0].Status, "#1 is untouched by a payment linked to #2")
	assert.Equal(t, InstallmentStatusPaid, statuses[1].Status)

	unlinked := ComputeInstallmentStatusesFromPayments(installments, []models.Payment{{Amount: 40000}}, now)
	assert.Equal(t, InstallmentStatusPaid, unlinked[0].Status, "unlinked money still waterfalls earliest-first")
	assert.Equal(t, InstallmentStatusPartial, unlinked[1].Status)
	assert.InDelta(t, 10000.0, unlinked[1].Covered, 0.001)
}

// Linked excess over its installment rejoins the waterfall; WHT counts as
// settled; a link to an installment not in the set is treated as unlinked.
func TestComputeInstallmentStatusesFromPayments_ExcessWhtAndStaleLink(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	installments := []models.PaymentInstallment{
		{HardDeleteModel: models.HardDeleteModel{ID: 1}, Amount: 10000, DueDate: daysFromNow(now, -10)},
		{HardDeleteModel: models.HardDeleteModel{ID: 2}, Amount: 10000, DueDate: daysFromNow(now, 10)},
		{HardDeleteModel: models.HardDeleteModel{ID: 3}, Amount: 10000, DueDate: daysFromNow(now, 40)},
	}
	two, gone := uint(2), uint(99)
	payments := []models.Payment{
		{Amount: 14700, WhtAmount: 300, InstallmentID: &two}, // settles 15000: #2 + 5000 excess
		{Amount: 4850, WhtAmount: 150, InstallmentID: &gone}, // stale link: plain 5000
	}
	statuses := ComputeInstallmentStatusesFromPayments(installments, payments, now)
	assert.Equal(t, InstallmentStatusPaid, statuses[0].Status, "#1 gets 5000 excess + 5000 unlinked")
	assert.Equal(t, InstallmentStatusPaid, statuses[1].Status)
	assert.Equal(t, InstallmentStatusUpcoming, statuses[2].Status)

	// No links at all: identical to the legacy total-paid form.
	plain := []models.Payment{{Amount: 9700, WhtAmount: 300}, {Amount: 5000}}
	assert.Equal(t, ComputeInstallmentStatuses(installments, 15000, now), ComputeInstallmentStatusesFromPayments(installments, plain, now))
}

func TestAgingBucketAndDaysOverdue(t *testing.T) {
	cases := map[int]string{-3: AgingCurrent, 0: AgingCurrent, 1: Aging1To30, 30: Aging1To30, 31: Aging31To60,
		60: Aging31To60, 61: Aging61To90, 90: Aging61To90, 91: Aging90Plus, 400: Aging90Plus}
	for days, want := range cases {
		assert.Equal(t, want, AgingBucket(days), "days=%d", days)
	}

	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.Local)
	assert.Equal(t, 0, DaysOverdue(time.Date(2026, 9, 27, 0, 0, 0, 0, time.Local), now))
	assert.Equal(t, 31, DaysOverdue(time.Date(2026, 8, 27, 23, 0, 0, 0, time.Local), now))
	assert.Equal(t, -3, DaysOverdue(time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local), now))
}

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

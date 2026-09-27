package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// Receivable comes from the Accepted Quote incl. VAT, before WHT; cash and
// WHT both settle it.
func TestComputeOutstandingRow_QuoteReceivableInclVatSettledInclWht(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.Local)
	quote := &models.Quote{
		Items:         models.JSONItems{{Description: "Build", Qty: 1, Price: 110000}},
		DiscountTotal: 10000, VatEnabled: true, WhtEnabled: true, WhtRate: 3,
	}
	// taxable 100,000 + VAT 7,000 = 107,000 receivable (WHT 3,000 is still owed, paid to RD).
	payments := []models.Payment{{Amount: 50000, WhtAmount: 1500}, {Amount: 20000}}

	r := outstandingBalanceRow{DealValue: 100000}
	computeOutstandingRow(&r, quote, payments, nil, now)

	assert.Equal(t, ReceivableSourceQuote, r.ReceivableSource)
	assert.InDelta(t, 107000, r.ReceivableAmount, 0.001)
	assert.InDelta(t, 70000, r.PaidAmount, 0.001)
	assert.InDelta(t, 1500, r.WhtAmount, 0.001)
	assert.InDelta(t, 107000-70000-1500, r.OutstandingAmount, 0.001)
	assert.Equal(t, OutstandingBalanceAgingNone, r.Aging)
	assert.Equal(t, utils.AgingCurrent, r.AgingBucket)
	assert.Nil(t, r.OldestOverdueDueDate)

	// Customer paid net of 3% WHT with the certificate: fully settled.
	full := outstandingBalanceRow{DealValue: 100000}
	computeOutstandingRow(&full, quote, []models.Payment{{Amount: 104000, WhtAmount: 3000}}, nil, now)
	assert.InDelta(t, 0, full.OutstandingAmount, 0.001)
}

func TestComputeOutstandingRow_FallsBackToDealValue(t *testing.T) {
	r := outstandingBalanceRow{DealValue: 5000}
	computeOutstandingRow(&r, nil, []models.Payment{{Amount: 1000}}, nil, time.Now())
	assert.Equal(t, ReceivableSourceDealValue, r.ReceivableSource)
	assert.InDelta(t, 5000, r.ReceivableAmount, 0.001)
	assert.InDelta(t, 4000, r.OutstandingAmount, 0.001)
}

// An Accepted quote that's an upload whose extraction failed has no items;
// it must not zero the receivable and hide an unpaid Won Deal.
func TestComputeOutstandingRow_UnpricedAcceptedQuoteFallsBackToDealValue(t *testing.T) {
	failed := "failed"
	file := "quote.pdf"
	quote := &models.Quote{FileName: &file, ExtractionStatus: &failed, VatEnabled: true}
	r := outstandingBalanceRow{DealValue: 80000}
	computeOutstandingRow(&r, quote, []models.Payment{{Amount: 30000}}, nil, time.Now())
	assert.Equal(t, ReceivableSourceDealValue, r.ReceivableSource)
	assert.InDelta(t, 80000, r.ReceivableAmount, 0.001)
	assert.InDelta(t, 50000, r.OutstandingAmount, 0.001)
}

// Aging uses the oldest unpaid overdue installment, after allocation.
func TestComputeOutstandingRow_AgingBuckets(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.Local)
	day := func(offset int) time.Time {
		return time.Date(2026, 9, 27, 0, 0, 0, 0, time.Local).AddDate(0, 0, offset)
	}
	inst := func(id uint, amount float64, due time.Time) models.PaymentInstallment {
		return models.PaymentInstallment{HardDeleteModel: models.HardDeleteModel{ID: id}, Amount: amount, DueDate: due}
	}

	cases := []struct {
		name         string
		installments []models.PaymentInstallment
		payments     []models.Payment
		wantBucket   string
		wantDays     int
		wantDue      *time.Time
		wantAging    string
	}{
		{"nothing due yet", []models.PaymentInstallment{inst(1, 1000, day(10))}, nil, utils.AgingCurrent, 0, nil, OutstandingBalanceAgingUpcoming},
		{"first installment paid, second 45 days late",
			[]models.PaymentInstallment{inst(1, 1000, day(-80)), inst(2, 1000, day(-45))},
			[]models.Payment{{Amount: 1000}}, utils.Aging31To60, 45, ptrTime(day(-45)), OutstandingBalanceAgingOverdue},
		{"oldest of two overdue wins", []models.PaymentInstallment{inst(1, 1000, day(-95)), inst(2, 1000, day(-5))},
			nil, utils.Aging90Plus, 95, ptrTime(day(-95)), OutstandingBalanceAgingOverdue},
		{"linked payment clears the older one", []models.PaymentInstallment{inst(1, 1000, day(-70)), inst(2, 1000, day(-20))},
			[]models.Payment{{Amount: 1000, InstallmentID: uintPtr(1)}}, utils.Aging1To30, 20, ptrTime(day(-20)), OutstandingBalanceAgingOverdue},
		{"61-90", []models.PaymentInstallment{inst(1, 1000, day(-61))}, nil, utils.Aging61To90, 61, ptrTime(day(-61)), OutstandingBalanceAgingOverdue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := outstandingBalanceRow{DealValue: 2000}
			computeOutstandingRow(&r, nil, tc.payments, tc.installments, now)
			assert.Equal(t, tc.wantBucket, r.AgingBucket)
			assert.Equal(t, tc.wantDays, r.DaysOverdue)
			assert.Equal(t, tc.wantAging, r.Aging)
			if tc.wantDue == nil {
				assert.Nil(t, r.OldestOverdueDueDate)
			} else {
				require.NotNil(t, r.OldestOverdueDueDate)
				assert.True(t, tc.wantDue.Equal(*r.OldestOverdueDueDate))
			}
		})
	}
}

func TestMergeSourcePerformance(t *testing.T) {
	rows := mergeSourcePerformance(
		[]sourcePerformanceRow{
			{Source: "Website", Leads: 10, Qualified: 4, DealsWon: 2, WonValue: 200000},
			{Source: "Referral", Leads: 10, Qualified: 6, DealsWon: 5, WonValue: 500000},
			{Source: "Event", Leads: 3},
		},
		[]sourcePerformanceRow{
			{Source: "Website", DirectDealsWon: 1, DirectWonValue: 50000},
			{Source: "Other", DirectDealsWon: 2, DirectWonValue: 80000},
		},
	)
	require.Len(t, rows, 4)
	// Leads desc, then total won value desc.
	assert.Equal(t, []string{"Referral", "Website", "Event", "Other"}, []string{rows[0].Source, rows[1].Source, rows[2].Source, rows[3].Source})
	assert.InDelta(t, 50, rows[0].WinRate, 0.001)
	assert.InDelta(t, 20, rows[1].WinRate, 0.001)
	assert.EqualValues(t, 1, rows[1].DirectDealsWon)
	assert.InDelta(t, 0, rows[3].WinRate, 0.001, "no leads: win rate 0, not NaN")
	assert.EqualValues(t, 2, rows[3].DirectDealsWon)
}

func TestDuplicateQuoteDates(t *testing.T) {
	now := time.Date(2026, 9, 27, 15, 0, 0, 0, time.Local)
	s := func(v string) *string { return &v }

	issue, validity := duplicateQuoteDates(models.Quote{IssueDate: s("2026-01-10"), ValidityDate: s("2026-02-09T00:00:00Z"), CreditDays: 7}, now)
	assert.Equal(t, "2026-09-27", issue)
	require.NotNil(t, validity)
	assert.Equal(t, "2026-10-27", *validity, "keeps the original 30-day issue→validity gap")

	_, validity = duplicateQuoteDates(models.Quote{ValidityDate: s("2026-02-09"), CreditDays: 15}, now)
	require.NotNil(t, validity)
	assert.Equal(t, "2026-10-12", *validity, "no issue date: falls back to credit_days")

	_, validity = duplicateQuoteDates(models.Quote{}, now)
	assert.Nil(t, validity, "nothing to derive a term from")
}

func ptrTime(t time.Time) *time.Time { return &t }
func uintPtr(v uint) *uint           { return &v }

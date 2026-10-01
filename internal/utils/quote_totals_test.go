package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// TestComputeQuoteTotals_MatchesReferenceExample reproduces the worked
// example from the quotation-builder reference screenshots: one item priced
// 46,650.00 with no discount, no quote-level discount, VAT enabled, WHT off
// -> subtotal 46,650.00, VAT 3,265.50, grand total 49,915.50.
func TestComputeQuoteTotals_MatchesReferenceExample(t *testing.T) {
	items := []models.QuoteItem{{Qty: 1, Price: 46650}}

	totals := ComputeQuoteTotals(items, 0, models.QuotePriceTypeExclTax, true, false, 0)

	assert.InDelta(t, 46650.0, totals.Subtotal, 0.001)
	assert.InDelta(t, 46650.0, totals.TaxableAmount, 0.001)
	assert.InDelta(t, 3265.50, totals.Vat, 0.001)
	assert.InDelta(t, 0.0, totals.Wht, 0.001)
	assert.InDelta(t, 49915.50, totals.GrandTotal, 0.001)
}

func TestComputeQuoteTotals_PerItemDiscount(t *testing.T) {
	// 2 x 1000 with a 10% line discount -> 1800, no VAT/WHT/quote discount.
	items := []models.QuoteItem{{Qty: 2, Price: 1000, DiscountPercent: 10}}

	totals := ComputeQuoteTotals(items, 0, models.QuotePriceTypeExclTax, false, false, 0)

	assert.InDelta(t, 1800.0, totals.Subtotal, 0.001)
	assert.InDelta(t, 1800.0, totals.GrandTotal, 0.001)
}

func TestComputeQuoteTotals_QuoteLevelDiscountAndWht(t *testing.T) {
	// Subtotal 10,000, flat discount 1,000 -> taxable 9,000.
	// VAT off, WHT 3% of the taxable amount withheld from what's paid.
	items := []models.QuoteItem{{Qty: 1, Price: 10000}}

	totals := ComputeQuoteTotals(items, 1000, models.QuotePriceTypeExclTax, false, true, 3)

	assert.InDelta(t, 10000.0, totals.Subtotal, 0.001)
	assert.InDelta(t, 9000.0, totals.TaxableAmount, 0.001)
	assert.InDelta(t, 0.0, totals.Vat, 0.001)
	assert.InDelta(t, 270.0, totals.Wht, 0.001)
	assert.InDelta(t, 8730.0, totals.GrandTotal, 0.001)
}

func TestComputeQuoteTotals_EmptyItems(t *testing.T) {
	totals := ComputeQuoteTotals(nil, 0, models.QuotePriceTypeExclTax, true, false, 0)
	assert.Equal(t, QuoteTotals{}, totals)
}

// TestComputeQuoteTotals_PriceTypeByVat covers excl/incl tax × VAT on/off
// (WHT on throughout, always taken from the pre-VAT amount). An incl_tax
// price already contains VAT, so it's backed out rather than added a second
// time; the customer is never quoted more than the item prices.
func TestComputeQuoteTotals_PriceTypeByVat(t *testing.T) {
	// 11,770 of items less a 1,070 quote discount = 10,700 net.
	items := []models.QuoteItem{{Qty: 1, Price: 11770}}
	cases := []struct {
		name       string
		priceType  models.QuotePriceType
		vatEnabled bool
		taxable    float64
		vat        float64
		wht        float64
		grandTotal float64
		receivable float64
	}{
		{"excl_tax, VAT on: VAT added on top", models.QuotePriceTypeExclTax, true, 10700, 749, 321, 11128, 11449},
		{"excl_tax, VAT off", models.QuotePriceTypeExclTax, false, 10700, 0, 321, 10379, 10700},
		{"incl_tax, VAT on: VAT backed out", models.QuotePriceTypeInclTax, true, 10000, 700, 300, 10400, 10700},
		{"incl_tax, VAT off: nothing to back out", models.QuotePriceTypeInclTax, false, 10700, 0, 321, 10379, 10700},
		{"unset price type behaves as excl_tax", "", true, 10700, 749, 321, 11128, 11449},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			totals := ComputeQuoteTotals(items, 1070, tc.priceType, tc.vatEnabled, true, 3)
			assert.InDelta(t, 11770.0, totals.Subtotal, 0.001)
			assert.InDelta(t, tc.taxable, totals.TaxableAmount, 0.001)
			assert.InDelta(t, tc.vat, totals.Vat, 0.001)
			assert.InDelta(t, tc.wht, totals.Wht, 0.001)
			assert.InDelta(t, tc.grandTotal, totals.GrandTotal, 0.001)
			assert.InDelta(t, tc.receivable, totals.ReceivableAmount(), 0.001)
		})
	}
}

// Backing VAT out rounds to satang, and taxable + VAT still adds back up to
// exactly the quoted (VAT-inclusive) amount.
func TestComputeQuoteTotals_InclTaxRoundsToSatang(t *testing.T) {
	totals := ComputeQuoteTotals([]models.QuoteItem{{Qty: 1, Price: 1000}}, 0, models.QuotePriceTypeInclTax, true, false, 0)
	assert.Equal(t, 934.58, totals.TaxableAmount) // 1000 / 1.07 = 934.5794...
	assert.Equal(t, 65.42, totals.Vat)
	assert.InDelta(t, 1000.0, totals.ReceivableAmount(), 1e-9)
	assert.InDelta(t, 1000.0, totals.GrandTotal, 1e-9)
}

func TestRoundSatang_HalfUpLikeJavaScript(t *testing.T) {
	assert.Equal(t, 1234.57, RoundSatang(1234.5678))
	assert.Equal(t, 0.3, RoundSatang(0.1+0.2))
	// JS Math.round rounds a half toward +Inf (-1.5 -> -1), not away from 0.
	assert.Equal(t, -0.01, RoundSatang(-0.015))
	assert.Equal(t, -0.02, RoundSatang(-0.016))
}

func TestDealReceivable(t *testing.T) {
	amount, fromQuote := DealReceivable(5000, nil)
	assert.Equal(t, 5000.0, amount)
	assert.False(t, fromQuote)

	// An Accepted quote with nothing priced (failed upload) falls back too.
	amount, fromQuote = DealReceivable(5000, &models.Quote{VatEnabled: true})
	assert.Equal(t, 5000.0, amount)
	assert.False(t, fromQuote)

	excl := &models.Quote{Items: models.JSONItems{{Qty: 1, Price: 333.333}}, PriceType: models.QuotePriceTypeExclTax, VatEnabled: true, WhtEnabled: true, WhtRate: 3}
	amount, fromQuote = DealReceivable(0, excl)
	// Line 333.33 (rounded per line) + VAT 23.33, before WHT.
	assert.Equal(t, 356.66, amount, "taxable + VAT, before WHT, rounded")
	assert.True(t, fromQuote)

	incl := &models.Quote{Items: models.JSONItems{{Qty: 1, Price: 107000}}, PriceType: models.QuotePriceTypeInclTax, VatEnabled: true}
	amount, _ = DealReceivable(0, incl)
	assert.Equal(t, 107000.0, amount, "an incl_tax quote is owed exactly its prices — no VAT on top")
}

// Every part is rounded to satang and the grand total is built from the
// rounded parts, so the PDF's printed lines add up exactly. Unrounded, three
// lines of 333.333 summed to 999.999 and the excl_tax grand total printed
// as 1,040.00 under parts that add up to 1,039.99.
func TestComputeQuoteTotals_PartsAddUpToTheSatang(t *testing.T) {
	items := []models.QuoteItem{{Qty: 1, Price: 333.333}, {Qty: 1, Price: 333.333}, {Qty: 1, Price: 333.333}}

	excl := ComputeQuoteTotals(items, 0, models.QuotePriceTypeExclTax, true, true, 3)
	assert.Equal(t, 999.99, excl.Subtotal)
	assert.Equal(t, 999.99, excl.TaxableAmount)
	assert.Equal(t, 70.0, excl.Vat)
	assert.Equal(t, 30.0, excl.Wht)
	assert.Equal(t, 1039.99, excl.GrandTotal)
	assert.Equal(t, 1069.99, RoundSatang(excl.ReceivableAmount()))

	incl := ComputeQuoteTotals(items, 0, models.QuotePriceTypeInclTax, true, true, 3)
	assert.Equal(t, 999.99, incl.Subtotal)
	assert.Equal(t, 934.57, incl.TaxableAmount)
	assert.Equal(t, 65.42, incl.Vat)
	assert.Equal(t, 28.04, incl.Wht)
	assert.Equal(t, 971.95, incl.GrandTotal)
	assert.Equal(t, 999.99, RoundSatang(incl.ReceivableAmount()))

	// A per-line discount is rounded per line too: 3 x 10.01 less 12.5% =
	// 26.27625 -> 26.28.
	disc := ComputeQuoteTotals([]models.QuoteItem{{Qty: 3, Price: 10.01, DiscountPercent: 12.5}}, 0.005, models.QuotePriceTypeExclTax, false, false, 0)
	assert.Equal(t, 26.28, disc.Subtotal)
	assert.Equal(t, 26.28, disc.TaxableAmount, "26.275 rounds half up")
	assert.Equal(t, 26.28, disc.GrandTotal)
}

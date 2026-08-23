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

	totals := ComputeQuoteTotals(items, 0, true, false, 0)

	assert.InDelta(t, 46650.0, totals.Subtotal, 0.001)
	assert.InDelta(t, 46650.0, totals.TaxableAmount, 0.001)
	assert.InDelta(t, 3265.50, totals.Vat, 0.001)
	assert.InDelta(t, 0.0, totals.Wht, 0.001)
	assert.InDelta(t, 49915.50, totals.GrandTotal, 0.001)
}

func TestComputeQuoteTotals_PerItemDiscount(t *testing.T) {
	// 2 x 1000 with a 10% line discount -> 1800, no VAT/WHT/quote discount.
	items := []models.QuoteItem{{Qty: 2, Price: 1000, DiscountPercent: 10}}

	totals := ComputeQuoteTotals(items, 0, false, false, 0)

	assert.InDelta(t, 1800.0, totals.Subtotal, 0.001)
	assert.InDelta(t, 1800.0, totals.GrandTotal, 0.001)
}

func TestComputeQuoteTotals_QuoteLevelDiscountAndWht(t *testing.T) {
	// Subtotal 10,000, flat discount 1,000 -> taxable 9,000.
	// VAT off, WHT 3% of the taxable amount withheld from what's paid.
	items := []models.QuoteItem{{Qty: 1, Price: 10000}}

	totals := ComputeQuoteTotals(items, 1000, false, true, 3)

	assert.InDelta(t, 10000.0, totals.Subtotal, 0.001)
	assert.InDelta(t, 9000.0, totals.TaxableAmount, 0.001)
	assert.InDelta(t, 0.0, totals.Vat, 0.001)
	assert.InDelta(t, 270.0, totals.Wht, 0.001)
	assert.InDelta(t, 8730.0, totals.GrandTotal, 0.001)
}

func TestComputeQuoteTotals_EmptyItems(t *testing.T) {
	totals := ComputeQuoteTotals(nil, 0, true, false, 0)
	assert.Equal(t, QuoteTotals{}, totals)
}

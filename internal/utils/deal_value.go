package utils

import (
	"math"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// DealValueFromQuote is the Deal value an Accepted quote syncs
// (api-system-spec.md §7.1/§7.4): its pre-VAT taxable amount rounded to
// satang. ok is false when the quote has no priced items (e.g. an uploaded
// PDF), which syncs nothing. Shared by the quote handlers and the
// boot-time backfill so both link a Deal on the same figure.
func DealValueFromQuote(q *models.Quote) (value float64, ok bool) {
	totals := QuoteTotalsOf(q)
	if totals.Subtotal <= 0 {
		return 0, false
	}
	return RoundSatang(totals.TaxableAmount), true
}

// SameMoney reports whether a and b are the same amount to within
// MoneyEpsilon.
func SameMoney(a, b float64) bool { return math.Abs(a-b) <= MoneyEpsilon }

package utils

import (
	"math"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// QuoteTotals is the fully-computed breakdown behind a Quote's summary block
// (both the quotation-builder edit page's live display and QuoteHandler.
// ExportPDF's totals section must agree with this — see ComputeQuoteTotals).
type QuoteTotals struct {
	Subtotal      float64 `json:"subtotal"`
	DiscountTotal float64 `json:"discount_total"`
	// TaxableAmount is always the pre-VAT amount (what counts as revenue) —
	// for an incl_tax quote it's the item prices with VAT backed out.
	TaxableAmount float64 `json:"taxable_amount"`
	Vat           float64 `json:"vat"`
	Wht           float64 `json:"wht"`
	GrandTotal    float64 `json:"grand_total"`
}

// quoteVatPercent is Thailand's statutory VAT rate — fixed, not configurable
// per quote (see Quote.VatEnabled's doc comment).
const quoteVatPercent = 7.0

// RoundSatang rounds a baht amount to whole satang (2 decimals), half up
// toward +Inf (floor(x*100 + 0.5)) — exactly like the frontend's roundSatang
// (Math.round(x*100)/100), negatives included, which Go's math.Round (half
// away from zero) would not match. The float64() conversion stops the
// compiler fusing x*100+0.5 into one FMA instruction (allowed on arm64),
// which would round differently from JavaScript at a few boundaries.
func RoundSatang(v float64) float64 { return math.Floor(float64(v*100)+0.5) / 100 }

// ComputeQuoteTotals derives a Quote's full totals breakdown from its items
// and quote-level discount/tax settings. Every figure is rounded to satang
// as it's produced, and the grand total is built from those rounded parts,
// so the lines a PDF prints add up exactly to its grand total:
//
//  1. Each line = roundSatang(qty*price*(1 - discountPercent/100));
//     Subtotal = roundSatang(sum of lines)
//  2. Net = roundSatang(Subtotal - discountTotal) (a further flat-amount
//     discount applied once across the whole quote)
//  3. VAT, only if vatEnabled:
//     - excl_tax (prices exclude VAT): TaxableAmount = Net,
//     Vat = roundSatang(TaxableAmount * 7 / 100)
//     - incl_tax (prices already include VAT): VAT is backed out, never
//     added on top — TaxableAmount = roundSatang(Net * 100 / 107),
//     Vat = roundSatang(Net - TaxableAmount), so TaxableAmount + Vat is
//     exactly Net (what the customer was quoted)
//     With VAT off, TaxableAmount = Net for either price type.
//  4. Wht = roundSatang(TaxableAmount * whtRate / 100), only if whtEnabled —
//     always on the pre-VAT amount, and withheld from (not added to) what
//     the customer actually pays
//  5. GrandTotal = roundSatang(TaxableAmount + Vat - Wht)
//
// The frontend mirrors this exact formula, operation for operation, in
// composables/utils/useQuoteTotals.ts — kept side-by-side commented on both
// ends specifically so the two can't silently drift apart.
func ComputeQuoteTotals(items []models.QuoteItem, discountTotal float64, priceType models.QuotePriceType, vatEnabled bool, whtEnabled bool, whtRate float64) QuoteTotals {
	var subtotal float64
	for _, item := range items {
		subtotal += QuoteLineTotal(item)
	}
	subtotal = RoundSatang(subtotal)

	net := RoundSatang(subtotal - discountTotal)
	taxable := net

	var vat, wht float64
	if vatEnabled {
		if priceType == models.QuotePriceTypeInclTax {
			taxable = RoundSatang(net * 100 / (100 + quoteVatPercent))
			vat = RoundSatang(net - taxable)
		} else {
			vat = RoundSatang(taxable * quoteVatPercent / 100)
		}
	}
	if whtEnabled {
		wht = RoundSatang(taxable * whtRate / 100)
	}

	return QuoteTotals{
		Subtotal:      subtotal,
		DiscountTotal: discountTotal,
		TaxableAmount: taxable,
		Vat:           vat,
		Wht:           wht,
		GrandTotal:    RoundSatang(taxable + vat - wht),
	}
}

// QuoteLineTotal is one line item's amount after its own discount, rounded
// to satang — the figure ComputeQuoteTotals sums and the PDF prints.
func QuoteLineTotal(item models.QuoteItem) float64 {
	lineTotal := item.Qty * item.Price
	if item.DiscountPercent > 0 {
		lineTotal *= 1 - item.DiscountPercent/100
	}
	return RoundSatang(lineTotal)
}

// QuoteTotalsOf is ComputeQuoteTotals over a loaded Quote's own fields.
func QuoteTotalsOf(q *models.Quote) QuoteTotals {
	return ComputeQuoteTotals(q.Items, q.DiscountTotal, q.PriceType, q.VatEnabled, q.WhtEnabled, q.WhtRate)
}

// ReceivableAmount is what the customer is invoiced for a Quote: the taxable
// amount after discounts plus VAT — before withholding tax, because WHT is
// still owed to us; the customer just pays that part to the Revenue
// Department instead (recorded as Payment.WhtAmount). GrandTotal is the net
// cash expected, so it would count WHT as never owed.
func (t QuoteTotals) ReceivableAmount() float64 { return t.TaxableAmount + t.Vat }

// DealReceivable is what the customer owes on a Deal — the Outstanding
// Balance rule: the latest Accepted Quote's ReceivableAmount, rounded to
// satang, when that Quote has priced line items; else the Deal value (no
// Accepted Quote, or one with nothing priced, e.g. an uploaded PDF whose
// extraction failed). The frontend's dealReceivable() is the same rule.
func DealReceivable(dealValue float64, latestAccepted *models.Quote) (amount float64, fromQuote bool) {
	if latestAccepted != nil {
		totals := QuoteTotalsOf(latestAccepted)
		if totals.Subtotal > 0 {
			return RoundSatang(totals.ReceivableAmount()), true
		}
	}
	return dealValue, false
}

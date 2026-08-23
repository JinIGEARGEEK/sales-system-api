package utils

import "github.com/igeargeek/sales-system-api/internal/models"

// QuoteTotals is the fully-computed breakdown behind a Quote's summary block
// (both the quotation-builder edit page's live display and QuoteHandler.
// ExportPDF's totals section must agree with this — see ComputeQuoteTotals).
type QuoteTotals struct {
	Subtotal      float64 `json:"subtotal"`
	DiscountTotal float64 `json:"discount_total"`
	TaxableAmount float64 `json:"taxable_amount"`
	Vat           float64 `json:"vat"`
	Wht           float64 `json:"wht"`
	GrandTotal    float64 `json:"grand_total"`
}

// quoteVatRate is Thailand's statutory VAT rate — fixed, not configurable
// per quote (see Quote.VatEnabled's doc comment).
const quoteVatRate = 0.07

// ComputeQuoteTotals derives a Quote's full totals breakdown from its items
// and quote-level discount/tax settings:
//
//  1. Subtotal = sum of each item's qty*price*(1 - discountPercent/100)
//  2. TaxableAmount = Subtotal - discountTotal (a further flat-amount
//     discount applied once across the whole quote)
//  3. Vat = TaxableAmount * 7%, only if vatEnabled
//  4. Wht = TaxableAmount * whtRate%, only if whtEnabled — withheld from
//     (not added to) what the customer actually pays
//  5. GrandTotal = TaxableAmount + Vat - Wht
//
// The frontend mirrors this exact formula in a TS computed (see
// components/Crm/AddQuoteModal.vue's successor quote editor) — kept
// side-by-side commented on both ends specifically so the two can't
// silently drift apart.
func ComputeQuoteTotals(items []models.QuoteItem, discountTotal float64, vatEnabled bool, whtEnabled bool, whtRate float64) QuoteTotals {
	var subtotal float64
	for _, item := range items {
		lineTotal := item.Qty * item.Price
		if item.DiscountPercent > 0 {
			lineTotal *= 1 - item.DiscountPercent/100
		}
		subtotal += lineTotal
	}

	taxable := subtotal - discountTotal

	var vat, wht float64
	if vatEnabled {
		vat = taxable * quoteVatRate
	}
	if whtEnabled {
		wht = taxable * whtRate / 100
	}

	return QuoteTotals{
		Subtotal:      subtotal,
		DiscountTotal: discountTotal,
		TaxableAmount: taxable,
		Vat:           vat,
		Wht:           wht,
		GrandTotal:    taxable + vat - wht,
	}
}

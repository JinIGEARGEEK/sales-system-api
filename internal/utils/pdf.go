package utils

import (
	"fmt"

	"github.com/go-pdf/fpdf"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// RenderLineItemsTable draws the Description/Qty/Unit Price/Total header row,
// one row per item, and a Grand Total row — used by Contract's PDF export
// (unchanged from before the quotation-builder rebuild). Quote's own export
// uses RenderQuoteItemsTable below instead, which adds a Discount % column;
// Contract deliberately keeps this simpler 4-column layout rather than
// picking up per-item discounts it has no field for. Header/party-info
// sections around this table differ enough between Quote and Contract to
// stay handler-specific.
func RenderLineItemsTable(pdf *fpdf.Fpdf, items []models.QuoteItem) float64 {
	return renderItemsTable(pdf, items, false, "Grand Total")
}

// RenderQuoteItemsTable is RenderLineItemsTable plus a Discount % column —
// Quote's PDF export uses this one so a discounted line item's PDF total
// matches what the edit page's live total shows (both ultimately derive from
// the same item.Qty/item.Price/item.DiscountPercent). Its bottom row is
// labeled "Subtotal", not "Grand Total": QuoteHandler.ExportPDF adds its own
// discount-total/VAT/WHT/grand-total rows immediately below this table.
func RenderQuoteItemsTable(pdf *fpdf.Fpdf, items []models.QuoteItem) float64 {
	return renderItemsTable(pdf, items, true, "Subtotal")
}

func renderItemsTable(pdf *fpdf.Fpdf, items []models.QuoteItem, showDiscount bool, totalLabel string) float64 {
	descColWidth := 90.0
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(descColWidth, 8, "Description", "1", 0, "L", false, 0, "")
	pdf.CellFormat(20, 8, "Qty", "1", 0, "R", false, 0, "")
	pdf.CellFormat(30, 8, "Unit Price", "1", 0, "R", false, 0, "")
	if showDiscount {
		descColWidth = 75.0
		pdf.CellFormat(20, 8, "Disc. %", "1", 0, "R", false, 0, "")
	}
	pdf.CellFormat(30, 8, "Total", "1", 1, "R", false, 0, "")

	pdf.SetFont("Arial", "", 10)
	const lineHeight = 5.0
	var grandTotal float64
	for _, item := range items {
		lineTotal := item.Qty * item.Price
		if showDiscount && item.DiscountPercent > 0 {
			lineTotal *= 1 - item.DiscountPercent/100
		}
		grandTotal += lineTotal

		// Descriptions can now run to several lines (e.g. a per-item scope-of-work
		// paragraph, not just a short label) — a plain CellFormat silently clips
		// anything past its fixed 8mm height, so size the row to fit first, then
		// draw a MultiCell inside a manually-drawn border matching that height.
		// SplitLines/MultiCell are given the same width so their wrapping agrees.
		lines := pdf.SplitLines([]byte(item.Description), descColWidth)
		rowHeight := float64(len(lines)) * lineHeight
		if rowHeight < 8 {
			rowHeight = 8
		}

		x, y := pdf.GetXY()
		pdf.Rect(x, y, descColWidth, rowHeight, "D")
		pdf.MultiCell(descColWidth, lineHeight, item.Description, "", "L", false)
		pdf.SetXY(x+descColWidth, y)
		pdf.CellFormat(20, rowHeight, fmt.Sprintf("%.0f", item.Qty), "1", 0, "R", false, 0, "")
		pdf.CellFormat(30, rowHeight, fmt.Sprintf("%.2f", item.Price), "1", 0, "R", false, 0, "")
		if showDiscount {
			pdf.CellFormat(20, rowHeight, fmt.Sprintf("%.1f", item.DiscountPercent), "1", 0, "R", false, 0, "")
		}
		pdf.CellFormat(30, rowHeight, fmt.Sprintf("%.2f", lineTotal), "1", 1, "R", false, 0, "")
	}
	pdf.SetFont("Arial", "B", 10)
	labelWidth := 150.0
	if showDiscount {
		labelWidth = 165.0
	}
	pdf.CellFormat(labelWidth, 8, totalLabel, "1", 0, "R", false, 0, "")
	pdf.CellFormat(30, 8, fmt.Sprintf("%.2f", grandTotal), "1", 1, "R", false, 0, "")

	return grandTotal
}

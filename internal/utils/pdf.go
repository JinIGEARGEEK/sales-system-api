package utils

import (
	"fmt"

	"github.com/go-pdf/fpdf"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// RenderLineItemsTable draws the Description/Qty/Unit Price/Total header row,
// one row per item, and a Grand Total row — the line-items table shared by
// Quote and Contract PDF exports. Returns the grand total in case a caller
// wants it (neither current caller does, but it's the natural output of this
// loop). Header/party-info sections around this table differ enough between
// Quote and Contract to stay handler-specific.
func RenderLineItemsTable(pdf *fpdf.Fpdf, items []models.QuoteItem) float64 {
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(90, 8, "Description", "1", 0, "L", false, 0, "")
	pdf.CellFormat(25, 8, "Qty", "1", 0, "R", false, 0, "")
	pdf.CellFormat(35, 8, "Unit Price", "1", 0, "R", false, 0, "")
	pdf.CellFormat(35, 8, "Total", "1", 1, "R", false, 0, "")

	pdf.SetFont("Arial", "", 10)
	var grandTotal float64
	for _, item := range items {
		lineTotal := item.Qty * item.Price
		grandTotal += lineTotal
		pdf.CellFormat(90, 8, item.Description, "1", 0, "L", false, 0, "")
		pdf.CellFormat(25, 8, fmt.Sprintf("%.0f", item.Qty), "1", 0, "R", false, 0, "")
		pdf.CellFormat(35, 8, fmt.Sprintf("%.2f", item.Price), "1", 0, "R", false, 0, "")
		pdf.CellFormat(35, 8, fmt.Sprintf("%.2f", lineTotal), "1", 1, "R", false, 0, "")
	}
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(150, 8, "Grand Total", "1", 0, "R", false, 0, "")
	pdf.CellFormat(35, 8, fmt.Sprintf("%.2f", grandTotal), "1", 1, "R", false, 0, "")

	return grandTotal
}

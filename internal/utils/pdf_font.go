package utils

import (
	_ "embed"

	"github.com/go-pdf/fpdf"
)

// PDFFont is the font family every generated PDF (Quote, Contract) uses:
// Sarabun, a Thai + Latin face (SIL Open Font License 1.1 — fonts/OFL.txt),
// embedded in the binary. fpdf's core fonts (Arial etc.) are cp1252 only, so
// any Thai text — company names, addresses, scope of work — used to come out
// as garbage.
const PDFFont = "Sarabun"

var (
	//go:embed fonts/Sarabun-Regular.ttf
	sarabunRegular []byte
	//go:embed fonts/Sarabun-Bold.ttf
	sarabunBold []byte
)

// NewPDF returns an A4 portrait fpdf document with PDFFont registered in
// regular ("") , bold ("B") and italic ("I") styles. There is no italic TTF
// embedded; "I" maps to the regular face so existing SetFont(…, "I", …)
// calls render upright rather than failing.
func NewPDF() *fpdf.Fpdf {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes(PDFFont, "", sarabunRegular)
	pdf.AddUTF8FontFromBytes(PDFFont, "B", sarabunBold)
	pdf.AddUTF8FontFromBytes(PDFFont, "I", sarabunRegular)
	return pdf
}

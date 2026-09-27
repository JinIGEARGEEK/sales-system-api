package utils

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/igeargeek/sales-system-api/internal/models"
)

func TestCompanyPartyLines(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name    string
		company models.Company
		want    []string
	}{
		{"nothing set", models.Company{}, nil},
		{"head office", models.Company{Address: str("1 Silom Rd, Bangkok"), PostalCode: str("10500"), TaxID: str("0105555555555"), BranchCode: str("00000")},
			[]string{"Address: 1 Silom Rd, Bangkok 10500", "Tax ID: 0105555555555 (สำนักงานใหญ่)"}},
		{"numbered branch", models.Company{TaxID: str("0105555555555"), BranchCode: str("00001")},
			[]string{"Tax ID: 0105555555555 (สาขาที่ 00001)"}},
		{"tax ID without branch", models.Company{TaxID: str("0105555555555")},
			[]string{"Tax ID: 0105555555555"}},
		{"branch without tax ID", models.Company{BranchCode: str("00002")},
			[]string{"Branch: สาขาที่ 00002"}},
		{"head office without tax ID", models.Company{BranchCode: str("00000")},
			[]string{"Branch: สำนักงานใหญ่"}},
		{"postal code only", models.Company{PostalCode: str("10110")},
			[]string{"Address: 10110"}},
	}
	for _, tc := range cases {
		if got := CompanyPartyLines(tc.company); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: CompanyPartyLines() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestNewPDF_RendersThai guards the embedded font: a Thai string must go
// through a document built by NewPDF without fpdf erroring, and the font
// must actually be embedded in the output (core Arial can't encode Thai at
// all — it used to print mojibake).
func TestNewPDF_RendersThai(t *testing.T) {
	pdf := NewPDF()
	pdf.AddPage()
	pdf.SetFont(PDFFont, "B", 14)
	pdf.Cell(0, 10, "ใบเสนอราคา บริษัท ตัวอย่าง จำกัด (สำนักงานใหญ่)")
	pdf.Ln(10)
	pdf.SetFont(PDFFont, "", 10)
	pdf.MultiCell(0, 5, "ขอบเขตงาน: พัฒนาระบบ CRM — Phase 1", "", "L", false)
	pdf.SetFont(PDFFont, "I", 10)
	pdf.Cell(0, 6, "italic maps to regular")
	RenderQuoteItemsTable(pdf, []models.QuoteItem{{Description: "ค่าบริการรายปี", Qty: 1, Price: 12000}})

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("PDF with Thai text failed: %v", err)
	}
	if !bytes.Contains(bytes.ToLower(buf.Bytes()), []byte("sarabun")) {
		t.Error("output PDF does not embed the Sarabun font")
	}
}

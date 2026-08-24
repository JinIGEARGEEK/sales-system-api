package utils

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	fitz "github.com/gen2brain/go-fitz"
)

// FlowAccountItem is one line item read from a FlowAccount PDF's items table.
type FlowAccountItem struct {
	Description     string
	Qty             float64
	Price           float64
	DiscountPercent float64
}

// FlowAccountExtraction is the best-effort structured read of a FlowAccount
// quotation PDF export, field-mapped onto Quote's own columns. Nothing here
// blocks or fails the Upload handler that calls it — see
// ExtractFlowAccountQuote's own doc comment for when it returns an error at
// all. Warnings records anything not confidently read, or a computed
// mismatch worth a rep's second look, without stopping extraction of
// everything else; Quote.ExtractionStatus/ExtractionWarnings persist these.
type FlowAccountExtraction struct {
	ReferenceNumber string
	IssueDate       *time.Time
	ScopeOfWork     string
	Items           []FlowAccountItem
	VatEnabled      bool
	WhtEnabled      bool
	WhtRate         float64
	Notes           string
	Warnings        []string
}

// Status summarizes Warnings for Quote.ExtractionStatus: "ok" (nothing to
// flag) or "partial" (some fields extracted, some missing/suspect — see
// Warnings). ExtractFlowAccountQuote itself returning an error is the
// "failed" case; there's no struct value for it.
func (e *FlowAccountExtraction) Status() string {
	if len(e.Warnings) == 0 {
		return "ok"
	}
	return "partial"
}

// flowAccountGlyphBug fixes one reproducible bug in the "CSChatThai" font
// FlowAccount embeds in its exported quotation PDFs: the glyph for sara aa
// ("า", U+0E32) has no correct entry in the font's ToUnicode map, so MuPDF's
// text extraction (via go-fitz) reports it as U+FFFD, the standard "unknown
// character" placeholder, instead. Every other Thai character in this font
// maps correctly — confirmed by inspecting the font's raw per-glyph codes
// directly, not guessed from how the scrambled output looked. The "ำา"→"ำ"
// collapse fixes the one place this shows up as a false positive: the same
// broken glyph fires a second, spurious time immediately after sara am
// ("ำ", U+0E33), which never precedes สระอา in real Thai, so dropping it is
// always safe. If FlowAccount ever changes this export template's font,
// this is the one place that would need revisiting.
func flowAccountGlyphFix(text string) string {
	text = strings.ReplaceAll(text, "\uFFFD", "า")
	text = strings.ReplaceAll(text, "ำา", "ำ")
	return text
}

var (
	reFlowAccountHeaderRow = regexp.MustCompile(`#\s*รายละเอียด\s*จำนวน\s*ราคาต่อหน่วย\s*ส่วนลด\s*มูลค่า`)
	// The per-page company/customer header block FlowAccount repeats at the
	// bottom of every page's text stream — matched so it can be stripped
	// before slicing out item descriptions, since on a page break it lands
	// mid-description otherwise (see the trailing-text handling below).
	reFlowAccountFooterBlock = regexp.MustCompile(`(?s)บริษัท ไอเกียกีค จำกัด \(สำนักงานใหญ่\).*?หน้าที่\s*\d+\s*/\s*\d+`)
	// The Thai amount-in-words line printed just above the subtotal (e.g.
	// "(หนึ่งแสนเก้าหมื่นห้าพันเจ็ดร้อยสามบาทถ้วน)") — stripped so it doesn't
	// glue onto the last item's description.
	reFlowAccountAmountInWords = regexp.MustCompile(`\(.*?บาทถ้วน\)`)
	reFlowAccountReferenceNo = regexp.MustCompile(`เลขที่\s+(\S+)`)
	reFlowAccountIssueDate   = regexp.MustCompile(`วันที่\s+(\d{2})/(\d{2})/(\d{4})`)
	reFlowAccountScopeOfWork = regexp.MustCompile(`ชื่องาน\s+(.+?)\s+ผู้ติดต่อ`)
	reFlowAccountSubtotal    = regexp.MustCompile(`รวมเป็นเงิน\s+([\d,]+\.\d{2})\s*บาท`)
	reFlowAccountVat         = regexp.MustCompile(`ภาษีมูลค่าเพิ่ม\s+(\d+(?:\.\d+)?)%\s+([\d,]+\.\d{2})\s*บาท`)
	reFlowAccountWht         = regexp.MustCompile(`หัก\s*ณ\s*ที่จ่าย\s*(\d+(?:\.\d+)?)%\s+([\d,]+\.\d{2})\s*บาท`)
	reFlowAccountGrandTotal  = regexp.MustCompile(`จำนวนเงินรวมทั้งสิ้น\s+([\d,]+\.\d{2})\s*บาท`)
	reFlowAccountNotesBlock  = regexp.MustCompile(`(?s)หมายเหตุ\s+(.+?)\n\s*ในนาม`)
	reFlowAccountLeadingIdx  = regexp.MustCompile(`^\s*\d+\s*`)
	// One item row: qty, unit price, an optional discount cell (blank for
	// most quotes — FlowAccount's UI enters it as a percentage, so a
	// non-blank value is assumed to be one and flagged for a rep to
	// confirm, since this sample export had none to verify the format
	// against), then the line's value. Matches the row as FlowAccount
	// renders it — qty/price/discount/value on one line, immediately after
	// the row's own wrapped, possibly multi-line description.
	reFlowAccountItemRow = regexp.MustCompile(`(\d+(?:\.\d+)?)\s+([\d,]+\.\d{2})\s+(?:(\d+(?:\.\d+)?%?)\s+)?([\d,]+\.\d{2})`)
)

func parseFlowAccountMoney(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return v
}

// ExtractFlowAccountQuote reads a FlowAccount-exported quotation PDF and
// returns a best-effort structured read of it. It only ever returns an error
// when the file couldn't be opened as a PDF at all, or doesn't look like a
// FlowAccount quotation export in the first place (no "ใบเสนอราคา" title, no
// "เลขที่" reference line) — callers (the Upload handler) treat that as
// Quote.ExtractionStatus = "failed" and fall back to today's behavior:
// the file stays attached, nothing gets pre-filled, no error surfaces to the
// rep. A non-nil result with a non-empty Warnings list is "partial", not a
// failure — whatever it did find is still worth pre-filling.
func ExtractFlowAccountQuote(pdfBytes []byte) (*FlowAccountExtraction, error) {
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer func() { _ = doc.Close() }()

	var raw strings.Builder
	for n := 0; n < doc.NumPage(); n++ {
		text, err := doc.Text(n)
		if err != nil {
			return nil, fmt.Errorf("read page %d: %w", n, err)
		}
		raw.WriteString(text)
		raw.WriteString("\n")
	}
	text := flowAccountGlyphFix(raw.String())

	if !strings.Contains(text, "ใบเสนอราคา") || !reFlowAccountReferenceNo.MatchString(text) {
		return nil, fmt.Errorf("does not look like a FlowAccount quotation export")
	}

	out := &FlowAccountExtraction{}

	if m := reFlowAccountReferenceNo.FindStringSubmatch(text); m != nil {
		out.ReferenceNumber = m[1]
	} else {
		out.Warnings = append(out.Warnings, "reference number (เลขที่) not found")
	}

	if m := reFlowAccountIssueDate.FindStringSubmatch(text); m != nil {
		if t, err := time.Parse("2006-01-02", fmt.Sprintf("%s-%s-%s", m[3], m[2], m[1])); err == nil {
			out.IssueDate = &t
		} else {
			out.Warnings = append(out.Warnings, "issue date (วันที่) found but unparseable: "+m[0])
		}
	} else {
		out.Warnings = append(out.Warnings, "issue date (วันที่) not found")
	}

	if m := reFlowAccountScopeOfWork.FindStringSubmatch(text); m != nil {
		out.ScopeOfWork = strings.Join(strings.Fields(m[1]), " ")
	} else {
		out.Warnings = append(out.Warnings, "job title (ชื่องาน) not found — scope of work left blank")
	}

	if m := reFlowAccountVat.FindStringSubmatch(text); m != nil {
		out.VatEnabled = true
		if m[1] != "7" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("printed VAT rate is %s%%, not the system's fixed 7%% — verify manually", m[1]))
		}
	}
	if m := reFlowAccountWht.FindStringSubmatch(text); m != nil {
		out.WhtEnabled = true
		out.WhtRate, _ = strconv.ParseFloat(m[1], 64)
	}

	if m := reFlowAccountNotesBlock.FindStringSubmatch(text); m != nil {
		out.Notes = strings.TrimSpace(m[1])
	}

	// Items live between the last table header and the subtotal line. Strip
	// every repeated per-page header row and company/customer footer block
	// first, so neither ends up swallowed into an item's description — the
	// footer in particular lands mid-description whenever that item's row
	// happens to span a page break (see the trailing-text handling below).
	itemsRegion := text
	if loc := reFlowAccountSubtotal.FindStringIndex(itemsRegion); loc != nil {
		itemsRegion = itemsRegion[:loc[0]]
	}
	itemsRegion = reFlowAccountHeaderRow.ReplaceAllString(itemsRegion, "\n")
	itemsRegion = reFlowAccountFooterBlock.ReplaceAllString(itemsRegion, "\n")
	itemsRegion = reFlowAccountAmountInWords.ReplaceAllString(itemsRegion, "\n")

	matches := reFlowAccountItemRow.FindAllStringSubmatchIndex(itemsRegion, -1)
	if len(matches) == 0 {
		out.Warnings = append(out.Warnings, "no line items recognized — the file is still attached, add items manually")
	}
	prevEnd := 0
	for _, m := range matches {
		desc := strings.Join(strings.Fields(reFlowAccountLeadingIdx.ReplaceAllString(itemsRegion[prevEnd:m[0]], "")), " ")
		prevEnd = m[1]

		qty := parseFlowAccountMoney(itemsRegion[m[2]:m[3]])
		price := parseFlowAccountMoney(itemsRegion[m[4]:m[5]])
		discountPercent := 0.0
		if m[6] != -1 {
			raw := itemsRegion[m[6]:m[7]]
			discountPercent, _ = strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
			out.Warnings = append(out.Warnings, fmt.Sprintf("item %q has a non-blank discount column (%s) — assumed to be a percentage, please verify", desc, raw))
		}
		out.Items = append(out.Items, FlowAccountItem{Description: desc, Qty: qty, Price: price, DiscountPercent: discountPercent})
	}
	// Whatever's left after the last row (before the subtotal) is a
	// continuation of that row's own description, not a new item — this is
	// the normal case whenever the last item's description happens to wrap
	// across a page break, since the row's numeric cells print once, at the
	// top of the row, while the description keeps flowing underneath.
	if len(out.Items) > 0 {
		if trailing := strings.Join(strings.Fields(itemsRegion[prevEnd:]), " "); trailing != "" {
			last := &out.Items[len(out.Items)-1]
			last.Description = strings.TrimSpace(last.Description + " " + trailing)
		}
	}

	// Cross-check the PDF's own printed grand total against what these
	// fields recompute to (mirrors useQuoteTotals.ts/ComputeQuoteTotals) —
	// catches a misparsed number even when every field above "succeeded".
	// Quote.DiscountTotal isn't extracted (this template has no whole-quote
	// discount line separate from each item's own), so it's 0 here, matching
	// what the Upload handler leaves it as.
	if m := reFlowAccountGrandTotal.FindStringSubmatch(text); m != nil {
		printed := parseFlowAccountMoney(m[1])
		subtotal := 0.0
		for _, it := range out.Items {
			subtotal += it.Qty * it.Price * (1 - it.DiscountPercent/100)
		}
		taxable := subtotal
		total := taxable
		if out.VatEnabled {
			total += taxable * 0.07
		}
		if out.WhtEnabled {
			total -= taxable * out.WhtRate / 100
		}
		if diff := total - printed; diff > 1 || diff < -1 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("recomputed total (%.2f) doesn't match the PDF's printed total (%.2f) — check items/VAT/WHT", total, printed))
		}
	}

	return out, nil
}

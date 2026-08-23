package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The extraction logic above is split from ExtractFlowAccountQuote's own
// fitz.NewFromMemory/doc.Text calls only in spirit — go-fitz doesn't expose
// a way to build a Document from a raw text string, so these tests exercise
// the same regex/parsing pipeline directly against a fixture string shaped
// exactly like go-fitz's real Text() output for a FlowAccount export
// (glyph-bug placeholders included), rather than round-tripping an actual
// PDF. The fixture is a redacted stand-in — company/customer/contact names
// and item descriptions genericized — not a real quote; only the layout,
// the � placeholders in place of every สระอา, and the numbers matter.
func flowAccountFixtureText() string {
	// Deliberately mirrors real go-fitz output: paragraph-merged single-space
	// text per "line", the header row repeated per page, and the
	// company/customer footer block landing mid-description on the page
	// break inside item 2 — the same layout quirk the real sample has.
	page1 := "# ร�ยละเอียด จำ�นวน ร�ค�ต่อหน่วย ส่วนลด มูลค่�\n" +
		"1 ส่วนเว็บไซต์หลักของระบบตัวอย่าง\n" +
		"1 82,900.00   82,900.00\n" +
		"2 ระบบ CMS สำหรับจัดการเนื้อห�\n" +
		"บริษัท ไอเกียกีค จำกัด (สำนักงานใหญ่) 89/9 หมู่ที่ 5 ต.สุเทพ อ.เมืองเชียงใหม่ จ.เชียงใหม่ 50200 เบอร์มือถือ 0954864474\n" +
		"ลูกค้� บริษัท ตัวอย่าง จำกัด\n" +
		"ใบเสนอราคา\n" +
		"เลขที่ QT2026080099 วันที่ 14/08/2026 ผู้ขาย ทดสอบ ระบบ\n" +
		"ชื่องาน โครงการตัวอย่าง ผู้ติดต่อ คุณ ทดสอบ เบอร์โทร 0800000000\n" +
		"หน้าที่ 1/2\n"
	page2 := "# ร�ยละเอียด จำ�นวน ร�ค�ต่อหน่วย ส่วนลด มูลค่�\n" +
		"1 100,000.00   100,000.00\n" +
		"(หนึ่งแสนแปดหมื่นสองพันเก้าร้อยบาทถ้วน)\n" +
		"รวมเป็นเงิน 182,900.00 บ�ท\n" +
		"ภ�ษีมูลค่�เพิ่ม 7% 12,803.00 บ�ท\n" +
		"จำ�นวนเงินรวมทั้งสิ้น 195,703.00 บ�ท\n" +
		"หม�ยเหตุ ยืนยันร�ค�ภ�ยใน 30 วัน\n" +
		"ในน�ม บริษัท ตัวอย่าง จำกัด\n"
	return page1 + page2
}

func TestFlowAccountGlyphFix(t *testing.T) {
	fixed := flowAccountGlyphFix("ร�ยละเอียด จำ�นวน")
	assert.Equal(t, "รายละเอียด จำนวน", fixed)
}

// parseFlowAccountText runs the same field/table extraction
// ExtractFlowAccountQuote does, minus the PDF-opening step — used directly
// by these tests so they don't depend on go-fitz's own PDF I/O.
func parseFlowAccountText(t *testing.T, raw string) *FlowAccountExtraction {
	t.Helper()
	text := flowAccountGlyphFix(raw)
	require.True(t, strings.Contains(text, "ใบเสนอราคา"))
	require.True(t, reFlowAccountReferenceNo.MatchString(text))

	out := &FlowAccountExtraction{}
	if m := reFlowAccountReferenceNo.FindStringSubmatch(text); m != nil {
		out.ReferenceNumber = m[1]
	}
	if m := reFlowAccountIssueDate.FindStringSubmatch(text); m != nil {
		out.IssueDate = mustParseFlowAccountDate(t, m)
	}
	if m := reFlowAccountScopeOfWork.FindStringSubmatch(text); m != nil {
		out.ScopeOfWork = strings.Join(strings.Fields(m[1]), " ")
	}
	if m := reFlowAccountVat.FindStringSubmatch(text); m != nil {
		out.VatEnabled = true
	}
	if m := reFlowAccountWht.FindStringSubmatch(text); m != nil {
		out.WhtEnabled = true
	}
	if m := reFlowAccountNotesBlock.FindStringSubmatch(text); m != nil {
		out.Notes = strings.TrimSpace(m[1])
	}

	itemsRegion := text
	if loc := reFlowAccountSubtotal.FindStringIndex(itemsRegion); loc != nil {
		itemsRegion = itemsRegion[:loc[0]]
	}
	itemsRegion = reFlowAccountHeaderRow.ReplaceAllString(itemsRegion, "\n")
	itemsRegion = reFlowAccountFooterBlock.ReplaceAllString(itemsRegion, "\n")

	matches := reFlowAccountItemRow.FindAllStringSubmatchIndex(itemsRegion, -1)
	prevEnd := 0
	for _, m := range matches {
		desc := strings.Join(strings.Fields(reFlowAccountLeadingIdx.ReplaceAllString(itemsRegion[prevEnd:m[0]], "")), " ")
		prevEnd = m[1]
		qty := parseFlowAccountMoney(itemsRegion[m[2]:m[3]])
		price := parseFlowAccountMoney(itemsRegion[m[4]:m[5]])
		out.Items = append(out.Items, FlowAccountItem{Description: desc, Qty: qty, Price: price})
	}
	if len(out.Items) > 0 {
		if trailing := strings.Join(strings.Fields(itemsRegion[prevEnd:]), " "); trailing != "" {
			last := &out.Items[len(out.Items)-1]
			last.Description = strings.TrimSpace(last.Description + " " + trailing)
		}
	}
	return out
}

func mustParseFlowAccountDate(t *testing.T, m []string) *time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02", m[3]+"-"+m[2]+"-"+m[1])
	require.NoError(t, err)
	return &tm
}

func TestParseFlowAccountText(t *testing.T) {
	out := parseFlowAccountText(t, flowAccountFixtureText())

	assert.Equal(t, "QT2026080099", out.ReferenceNumber)
	require.NotNil(t, out.IssueDate)
	assert.Equal(t, "2026-08-14", out.IssueDate.Format("2006-01-02"))
	assert.Equal(t, "โครงการตัวอย่าง", out.ScopeOfWork)
	assert.True(t, out.VatEnabled)
	assert.False(t, out.WhtEnabled)
	assert.Contains(t, out.Notes, "ยืนยันราคาภายใน 30 วัน")

	require.Len(t, out.Items, 2)
	assert.Equal(t, "ส่วนเว็บไซต์หลักของระบบตัวอย่าง", out.Items[0].Description)
	assert.Equal(t, 1.0, out.Items[0].Qty)
	assert.Equal(t, 82900.0, out.Items[0].Price)
	// Item 2's description continues after the company/customer footer
	// block that lands between its two halves on the page break — the
	// trailing-text handling should have re-joined them.
	assert.Contains(t, out.Items[1].Description, "ระบบ CMS สำหรับจัดการเนื้อหา")
	assert.Equal(t, 1.0, out.Items[1].Qty)
	assert.Equal(t, 100000.0, out.Items[1].Price)
}

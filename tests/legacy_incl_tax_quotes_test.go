package apitests

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// seedInclTaxQuote is a 10,000 tax-inclusive-priced quote created at createdAt.
func seedInclTaxQuote(t *testing.T, db *gorm.DB, dealID uint, status models.QuoteStatus, vat bool, number string, createdAt time.Time) *models.Quote {
	t.Helper()
	q := &models.Quote{
		DealID: dealID, Status: status, Number: &number,
		Items:     models.JSONItems{{Description: "Build", Qty: 1, Price: 10000}},
		PriceType: models.QuotePriceTypeInclTax, VatEnabled: vat,
	}
	require.NoError(t, db.Create(q).Error)
	// UpdateColumns, not Create: GORM skips a false bool on insert, so the
	// column default (vat_enabled true) would win.
	require.NoError(t, db.Model(q).UpdateColumns(map[string]interface{}{"created_at": createdAt, "vat_enabled": vat}).Error)
	return q
}

func storedQuote(t *testing.T, db *gorm.DB, id uint) models.Quote {
	t.Helper()
	var q models.Quote
	require.NoError(t, db.First(&q, id).Error)
	return q
}

// Non-draft incl_tax+VAT quotes created before the fix switch to excl_tax,
// so they total exactly what the old rule printed (10,000 + 7% = 10,700);
// a deal value linked to one is unlinked but keeps its value. Drafts,
// VAT-off quotes and quotes created after the fix are untouched. Runs once.
func TestKeepLegacyInclTaxQuoteTotals(t *testing.T) {
	_, db := testutil.App(t)
	require.NoError(t, db.Where("name = ?", "legacy_incl_tax_quote_totals").Delete(&models.DataMigration{}).Error)
	before := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	after := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

	deal := seedDeal(t, db, nil)
	sent := seedInclTaxQuote(t, db, deal.ID, models.QuoteStatusSent, true, "QT-LV1", before)
	accepted := seedInclTaxQuote(t, db, deal.ID, models.QuoteStatusAccepted, true, "QT-LV2", before)
	require.NoError(t, db.Model(deal).Updates(map[string]interface{}{"value": 9345.79, "value_quote_id": accepted.ID}).Error)
	draft := seedInclTaxQuote(t, db, deal.ID, models.QuoteStatusDraft, true, "QT-LV3", before)
	noVat := seedInclTaxQuote(t, db, deal.ID, models.QuoteStatusSent, false, "QT-LV4", before)
	newer := seedInclTaxQuote(t, db, deal.ID, models.QuoteStatusSent, true, "QT-LV5", after)

	require.NoError(t, database.KeepLegacyInclTaxQuoteTotals(db))

	for _, q := range []*models.Quote{sent, accepted} {
		got := storedQuote(t, db, q.ID)
		assert.Equal(t, models.QuotePriceTypeExclTax, got.PriceType, *q.Number)
		totals := utils.QuoteTotalsOf(&got)
		assert.InDelta(t, 10000, totals.TaxableAmount, 0.001, *q.Number)
		assert.InDelta(t, 10700, totals.GrandTotal, 0.001, "%s keeps the pre-fix total", *q.Number)
	}
	for _, q := range []*models.Quote{draft, noVat, newer} {
		assert.Equal(t, models.QuotePriceTypeInclTax, storedQuote(t, db, q.ID).PriceType, *q.Number)
	}
	got := storedDeal(t, db, deal.ID)
	assert.Nil(t, got.ValueQuoteID, "unlinked from the converted quote")
	assert.InDelta(t, 9345.79, got.Value, 0.001, "revenue never rewritten")

	// Runs once: a quote set back to incl_tax afterwards stays that way.
	require.NoError(t, db.Model(&models.Quote{}).Where("id = ?", sent.ID).UpdateColumn("price_type", models.QuotePriceTypeInclTax).Error)
	require.NoError(t, database.KeepLegacyInclTaxQuoteTotals(db))
	assert.Equal(t, models.QuotePriceTypeInclTax, storedQuote(t, db, sent.ID).PriceType)
}

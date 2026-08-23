package apitests

import (
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// quoteEnvelope mirrors utils.OK/Created's {"data": ...} response wrapper.
type quoteEnvelope struct {
	Data models.Quote `json:"data"`
}

// TestQuoteCreate_GeneratesSequentialNumbers guards the quotation-builder
// rebuild's NextDocumentNumber integration: each created Quote gets a
// distinct "QT{YYYYMM}{seq}" number, and two quotes created back-to-back in
// the same month get consecutive sequence values.
func TestQuoteCreate_GeneratesSequentialNumbers(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	numberPattern := regexp.MustCompile(`^QT\d{6}\d{3}$`)

	var first, second quoteEnvelope
	req1 := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
		"items": []map[string]interface{}{{"description": "Line 1", "qty": 1, "price": 100}},
	}, admin.ID, admin.Role)
	resp1 := doJSON(t, app, req1, &first)
	require.Equal(t, http.StatusCreated, resp1.StatusCode)
	require.NotNil(t, first.Data.Number)
	assert.Regexp(t, numberPattern, *first.Data.Number)

	req2 := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
		"items": []map[string]interface{}{{"description": "Line 1", "qty": 1, "price": 100}},
	}, admin.ID, admin.Role)
	resp2 := doJSON(t, app, req2, &second)
	require.Equal(t, http.StatusCreated, resp2.StatusCode)
	require.NotNil(t, second.Data.Number)

	assert.NotEqual(t, *first.Data.Number, *second.Data.Number, "each created Quote must get a distinct number")

	prefix := "QT" + time.Now().Format("200601")
	assert.True(t, len(*first.Data.Number) == len(prefix)+3 && (*first.Data.Number)[:len(prefix)] == prefix,
		"number %q must start with this month's prefix %q", *first.Data.Number, prefix)
}

// TestQuoteCreate_DefaultsPriceTypeAndVat guards the new fields' documented
// defaults when a caller omits them entirely (every pre-existing client) —
// excl_tax pricing, VAT on by default (Thai VAT is normally charged unless a
// quote opts out), WHT off.
func TestQuoteCreate_DefaultsPriceTypeAndVat(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	var created quoteEnvelope
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
		"items": []map[string]interface{}{{"description": "Line 1", "qty": 1, "price": 100}},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	assert.Equal(t, models.QuotePriceTypeExclTax, created.Data.PriceType)
	assert.True(t, created.Data.VatEnabled)
	assert.False(t, created.Data.WhtEnabled)
	assert.Equal(t, 0, created.Data.CreditDays)
}

// TestQuoteCreate_RejectsNegativeNewFields guards validateQuoteForm's
// non-negative checks on the fields this rebuild added.
func TestQuoteCreate_RejectsNegativeNewFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	cases := []struct {
		name  string
		field string
		value interface{}
	}{
		{"credit_days", "credit_days", -1},
		{"wht_rate", "wht_rate", -3.0},
		{"discount_total", "discount_total", -50.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
				"items":  []map[string]interface{}{{"description": "Line 1", "qty": 1, "price": 100}},
				tc.field: tc.value,
			}, admin.ID, admin.Role)
			resp := doJSON(t, app, req, nil)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "%s=%v must be rejected", tc.field, tc.value)
		})
	}
}

// TestQuoteCreate_RejectsInvalidPriceType guards the price_type enum check.
func TestQuoteCreate_RejectsInvalidPriceType(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
		"items":      []map[string]interface{}{{"description": "Line 1", "qty": 1, "price": 100}},
		"price_type": "not_a_real_type",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestQuoteUpdate_PersistsNewFieldsAndAllowsClearingOptionalOnes guards the
// Update handler's new-field wiring end-to-end, including that Notes/
// ReferenceNumber can be explicitly cleared back to nil (unconditional
// assignment, same convention as ScopeOfWork).
func TestQuoteUpdate_PersistsNewFieldsAndAllowsClearingOptionalOnes(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	ref := "PO-123"
	quote := &models.Quote{
		DealID: deal.ID, Items: models.JSONItems{}, Status: models.QuoteStatusDraft,
		ReferenceNumber: &ref,
	}
	require.NoError(t, db.Create(quote).Error)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(quote.ID), map[string]interface{}{
		"status":         "sent",
		"credit_days":    14,
		"wht_enabled":    true,
		"wht_rate":       3.0,
		"discount_total": 200.0,
		"price_type":     "incl_tax",
		// reference_number omitted entirely -> explicitly cleared to nil,
		// same as how a rep clearing a text field sends no value for it.
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Quote
	require.NoError(t, db.First(&reloaded, quote.ID).Error)
	assert.Equal(t, 14, reloaded.CreditDays)
	assert.True(t, reloaded.WhtEnabled)
	assert.Equal(t, 3.0, reloaded.WhtRate)
	assert.Equal(t, 200.0, reloaded.DiscountTotal)
	assert.Equal(t, models.QuotePriceTypeInclTax, reloaded.PriceType)
	assert.Nil(t, reloaded.ReferenceNumber, "omitted reference_number must be cleared, not left untouched")
}

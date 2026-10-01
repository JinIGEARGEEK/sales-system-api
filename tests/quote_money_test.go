package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

type fieldsErrorBody struct {
	Error struct {
		Fields map[string][]string `json:"fields"`
	} `json:"error"`
}

func bulkInstallmentsRequest(t *testing.T, dealID uint, admin *models.User, amounts ...float64) *http.Request {
	t.Helper()
	rows := make([]map[string]interface{}, 0, len(amounts))
	for i, amount := range amounts {
		rows = append(rows, map[string]interface{}{"amount": amount, "due_date": time.Now().AddDate(0, i+1, 0).Format(time.RFC3339)})
	}
	return testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(dealID)+"/payment-installments/bulk", map[string]interface{}{
		"installments": rows,
	}, admin.ID, admin.Role)
}

// TestPaymentInstallmentBulkCreate_RejectsScheduleOverReceivable guards that
// a generated schedule can't plan more than the Deal's receivable once its
// existing installments are counted — and that a rejected batch inserts
// nothing.
func TestPaymentInstallmentBulkCreate_RejectsScheduleOverReceivable(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil) // value 1,000, no Accepted Quote
	require.NoError(t, db.Create(&models.PaymentInstallment{DealID: deal.ID, Amount: 600, DueDate: time.Now()}).Error)

	var errOut fieldsErrorBody
	resp := doJSON(t, app, bulkInstallmentsRequest(t, deal.ID, admin, 200, 200.01), &errOut)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, []string{"exceeds_receivable"}, errOut.Error.Fields["installments"])

	var count int64
	db.Model(&models.PaymentInstallment{}).Where("deal_id = ?", deal.ID).Count(&count)
	assert.EqualValues(t, 1, count, "a rejected batch must insert nothing")

	// Exactly the remaining 400 fits.
	resp = doJSON(t, app, bulkInstallmentsRequest(t, deal.ID, admin, 200, 200), nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// The receivable is the Outstanding Balance one: an Accepted Quote's
// taxable amount + VAT (here tax-inclusive, so exactly its prices) wins over
// the Deal value.
func TestPaymentInstallmentBulkCreate_ReceivableFromAcceptedQuote(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil) // value 1,000
	require.NoError(t, db.Create(&models.Quote{
		DealID: deal.ID, Status: models.QuoteStatusAccepted, PriceType: models.QuotePriceTypeInclTax, VatEnabled: true,
		Items: models.JSONItems{{Description: "Build", Qty: 1, Price: 10700}},
	}).Error)

	resp := doJSON(t, app, bulkInstallmentsRequest(t, deal.ID, admin, 5350, 5350.01), nil)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "10,700.01 > 10,700 (incl. VAT, not 10,700 + 7%)")

	resp = doJSON(t, app, bulkInstallmentsRequest(t, deal.ID, admin, 5350, 5350), nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// TestQuoteUpdate_AcceptedQuotePricingIsLocked guards that an Accepted
// Quote's items/pricing can't be edited (the Deal's receivable and revenue
// come from it), while resending the same values, changing its status, and
// editing non-money fields still work.
func TestQuoteUpdate_AcceptedQuotePricingIsLocked(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	quote := &models.Quote{
		DealID: deal.ID, Status: models.QuoteStatusAccepted, PriceType: models.QuotePriceTypeExclTax, VatEnabled: true,
		Items: models.JSONItems{{Description: "Build", Qty: 1, Price: 50000}},
	}
	require.NoError(t, db.Create(quote).Error)

	body := func(overrides map[string]interface{}) map[string]interface{} {
		b := map[string]interface{}{
			"status": "accepted", "scope_of_work": "Phase 1",
			"items":      []map[string]interface{}{{"description": "Build", "qty": 1, "price": 50000}},
			"price_type": "excl_tax", "vat_enabled": true, "wht_enabled": false, "wht_rate": 0, "discount_total": 0,
		}
		for k, v := range overrides {
			b[k] = v
		}
		return b
	}
	put := func(b map[string]interface{}) (*http.Response, map[string][]string) {
		var out fieldsErrorBody
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(quote.ID), b, admin.ID, admin.Role)
		resp := doJSON(t, app, req, &out)
		return resp, out.Error.Fields
	}
	repriced := []map[string]interface{}{{"description": "Build", "qty": 1, "price": 60000}}

	resp, fields := put(body(map[string]interface{}{"items": repriced}))
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, []string{"accepted_locked"}, fields["items"])

	resp, fields = put(body(map[string]interface{}{"price_type": "incl_tax", "discount_total": 100}))
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, fields, "price_type")
	assert.Contains(t, fields, "discount_total")

	// Same pricing resent with a non-money edit: fine.
	resp, _ = put(body(map[string]interface{}{"scope_of_work": "Phase 1 + 2"}))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Status can still move (here to Rejected) — after which pricing is
	// editable again.
	resp, _ = put(body(map[string]interface{}{"status": "rejected"}))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = put(body(map[string]interface{}{"status": "rejected", "items": repriced}))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Quote
	require.NoError(t, db.First(&reloaded, quote.ID).Error)
	assert.Equal(t, models.QuoteStatusRejected, reloaded.Status)
	assert.Equal(t, 60000.0, reloaded.Items[0].Price)
	assert.Equal(t, "Phase 1", reloaded.ScopeOfWork)
}

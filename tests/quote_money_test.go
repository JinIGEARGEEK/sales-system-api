package apitests

import (
	"net/http"
	"sync"
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

// TestPaymentInstallmentBulkCreate_ConcurrentBatchesStayWithinReceivable
// guards that the receivable cap is checked under the Deal row lock: several
// batches that each fit alone, but not together, sent at once can't all pass
// on the same "already scheduled" total.
func TestPaymentInstallmentBulkCreate_ConcurrentBatchesStayWithinReceivable(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil) // value 1,000, no Accepted Quote

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := app.Test(bulkInstallmentsRequest(t, deal.ID, admin, 300, 300), -1)
			if err == nil {
				codes[i] = resp.StatusCode
			}
		}(i)
	}
	wg.Wait()

	created := 0
	for _, code := range codes {
		if code == http.StatusCreated {
			created++
		} else {
			assert.Equal(t, http.StatusUnprocessableEntity, code)
		}
	}
	assert.Equal(t, 1, created, "only one 600 batch fits a 1,000 receivable")
	var total float64
	db.Model(&models.PaymentInstallment{}).Where("deal_id = ?", deal.ID).Select("COALESCE(SUM(amount), 0)").Scan(&total)
	assert.InDelta(t, 600, total, 0.001)
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
// Quote's pricing can't be edited (the Deal's receivable and revenue come
// from it): any content change is a 409 (quote lifecycle guard), resending
// the stored values is fine, and the quote can still move to Rejected, after
// which it stays read-only.
func TestQuoteUpdate_AcceptedQuotePricingIsLocked(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	quote := &models.Quote{
		DealID: deal.ID, Status: models.QuoteStatusAccepted, PriceType: models.QuotePriceTypeExclTax, VatEnabled: true,
		ScopeOfWork: "Phase 1",
		Items:       models.JSONItems{{Description: "Build", Qty: 1, Price: 50000}},
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
	put := func(b map[string]interface{}) *http.Response {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(quote.ID), b, admin.ID, admin.Role)
		return doJSON(t, app, req, nil)
	}
	repriced := []map[string]interface{}{{"description": "Build", "qty": 1, "price": 60000}}

	require.Equal(t, http.StatusConflict, put(body(map[string]interface{}{"items": repriced})).StatusCode)
	require.Equal(t, http.StatusConflict, put(body(map[string]interface{}{"price_type": "incl_tax", "discount_total": 100})).StatusCode)

	// Resending the stored values unchanged is fine.
	require.Equal(t, http.StatusOK, put(body(nil)).StatusCode)

	// Accepted → Rejected is allowed; a Rejected quote stays read-only.
	require.Equal(t, http.StatusOK, put(body(map[string]interface{}{"status": "rejected"})).StatusCode)
	require.Equal(t, http.StatusConflict, put(body(map[string]interface{}{"status": "rejected", "items": repriced})).StatusCode)

	var reloaded models.Quote
	require.NoError(t, db.First(&reloaded, quote.ID).Error)
	assert.Equal(t, models.QuoteStatusRejected, reloaded.Status)
	assert.Equal(t, 50000.0, reloaded.Items[0].Price)
}

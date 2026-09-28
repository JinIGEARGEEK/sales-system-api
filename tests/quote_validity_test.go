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

// TestQuote_ValidThroughItsLastLocalDay guards a Sent quote staying Sent
// all through its validity date (server-local), and the expiring-soon
// report listing it that day — not expiring at 07:00 Bangkok, the UTC
// midnight a bare date parses to.
func TestQuote_ValidThroughItsLastLocalDay(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	deal := seedDeal(t, db, nil)
	for _, v := range []string{today, yesterday} {
		v := v
		require.NoError(t, db.Create(&models.Quote{DealID: deal.ID, ValidityDate: &v, Status: models.QuoteStatusSent,
			Items: models.JSONItems{{Description: "Item", Qty: 2, Price: 50, DiscountPercent: 10}}, DiscountTotal: 10}).Error)
	}

	var list struct {
		Data []struct {
			ValidityDate string             `json:"validity_date"`
			Status       models.QuoteStatus `json:"status"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", nil, admin.ID, admin.Role), &list)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	statusByDate := map[string]models.QuoteStatus{}
	for _, q := range list.Data {
		statusByDate[q.ValidityDate] = q.Status
	}
	assert.Equal(t, models.QuoteStatusSent, statusByDate[today], "valid through today")
	assert.Equal(t, models.QuoteStatusExpired, statusByDate[yesterday])

	var soon struct {
		Data []struct {
			ValidityDate string  `json:"validity_date"`
			TotalValue   float64 `json:"total_value"`
		} `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/quotes-expiring-soon", nil, admin.ID, admin.Role), &soon)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, soon.Data, 1, "today's quote listed, yesterday's not")
	assert.Equal(t, today, soon.Data[0].ValidityDate)
	// (2 × 50 × 90% − 10) × 1.07 — the PDF's grand total, not raw qty × price.
	assert.InDelta(t, 85.6, soon.Data[0].TotalValue, 0.001)
}

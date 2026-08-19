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

// TestDashboardSummary_RevenueTrendBucketsByMonth guards revenueTrend's
// rewrite from 6 sequential per-month queries into a single grouped query —
// a wrong GROUP BY/date-window would silently misplace revenue into the
// wrong month bucket or drop it entirely.
func TestDashboardSummary_RevenueTrendBucketsByMonth(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	thisMonthDeal := seedDeal(t, db, nil)
	thisMonthDeal.Status = models.DealStatusWon
	thisMonthDeal.Value = 1000
	require.NoError(t, db.Save(thisMonthDeal).Error)

	twoMonthsAgoDeal := seedDeal(t, db, nil)
	twoMonthsAgoDeal.Status = models.DealStatusWon
	twoMonthsAgoDeal.Value = 500
	require.NoError(t, db.Save(twoMonthsAgoDeal).Error)
	pastDate := time.Now().AddDate(0, -2, 0)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", twoMonthsAgoDeal.ID).
		Update("created_at", pastDate).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			RevenueTrend []struct {
				Label string  `json:"label"`
				Value float64 `json:"value"`
			} `json:"revenue_trend"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, out.Data.RevenueTrend, 6, "revenue_trend must always report exactly 6 months")

	now := time.Now()
	byLabel := map[string]float64{}
	for _, p := range out.Data.RevenueTrend {
		byLabel[p.Label] += p.Value
	}
	assert.Equal(t, 1000.0, byLabel[now.Format("Jan")], "current month's won value")
	assert.Equal(t, 500.0, byLabel[pastDate.Format("Jan")], "two-months-ago won value")
}

// TestDashboardSummary_ForecastTrendBucketsByExpectedCloseDate guards
// forecastTrend's equivalent rewrite, including its LEFT(text, 7) grouping
// tolerating both stored date-string shapes.
func TestDashboardSummary_ForecastTrendBucketsByExpectedCloseDate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	nextMonth := time.Now().AddDate(0, 1, 0)
	dateStr := nextMonth.Format("2006-01-02")
	deal := seedDeal(t, db, nil)
	deal.Status = models.DealStatusOpen
	deal.Value = 2000
	prob := 50
	deal.Probability = &prob
	deal.ExpectedCloseDate = &dateStr
	require.NoError(t, db.Save(deal).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			ForecastTrend []struct {
				Label string  `json:"label"`
				Value float64 `json:"value"`
			} `json:"forecast_trend"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data.ForecastTrend, 6)

	byLabel := map[string]float64{}
	for _, p := range out.Data.ForecastTrend {
		byLabel[p.Label] += p.Value
	}
	assert.Equal(t, 1000.0, byLabel[nextMonth.Format("Jan")], "2000 * 50% probability")
}

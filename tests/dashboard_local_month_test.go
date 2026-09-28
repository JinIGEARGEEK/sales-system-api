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

// TestDashboardSummary_TrendsBucketByLocalMonth guards the trends splitting
// months at Bangkok midnight, not the DB session's UTC: a Deal won in the
// first seven hours of the 1st, or a close date stored as a JS Date
// ("…T17:00:00.000Z" the day before), belongs to the new month.
func TestDashboardSummary_TrendsBucketByLocalMonth(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, ictZone)
	nextMonth := monthStart.AddDate(0, 1, 0)

	// Won 00:30 Bangkok on this month's 1st: the previous month in UTC.
	won := seedDeal(t, db, nil)
	require.NoError(t, db.Model(won).Updates(map[string]interface{}{"status": models.DealStatusWon, "value": 700}).Error)
	setCreatedAt(t, db, &models.Deal{}, won.ID, monthStart.Add(30*time.Minute))
	// Won 23:30 Bangkok on the last day of the previous month.
	prevWon := seedDeal(t, db, nil)
	require.NoError(t, db.Model(prevWon).Updates(map[string]interface{}{"status": models.DealStatusWon, "value": 300}).Error)
	setCreatedAt(t, db, &models.Deal{}, prevWon.ID, monthStart.Add(-30*time.Minute))

	// Closes on next month's 1st, stored as the JS Date for local midnight.
	closeDate := nextMonth.UTC().Format("2006-01-02T15:04:05.000Z")
	open := seedDeal(t, db, nil)
	prob := 50
	require.NoError(t, db.Model(open).Updates(map[string]interface{}{"expected_close_date": closeDate, "probability": prob, "value": 2000}).Error)

	var out struct {
		Data struct {
			RevenueTrend []struct {
				Label string  `json:"label"`
				Value float64 `json:"value"`
			} `json:"revenue_trend"`
			ForecastTrend []struct {
				Label string  `json:"label"`
				Value float64 `json:"value"`
			} `json:"forecast_trend"`
			AnnualRevenueTrend []struct {
				Label  string  `json:"label"`
				Actual float64 `json:"actual"`
			} `json:"annual_revenue_trend"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, out.Data.RevenueTrend, 6)
	last := out.Data.RevenueTrend[5]
	assert.Equal(t, monthStart.Format("Jan"), last.Label)
	assert.Equal(t, 700.0, last.Value, "won at 00:30 on the 1st is this month")
	assert.Equal(t, monthStart.AddDate(0, -1, 0).Format("Jan"), out.Data.RevenueTrend[4].Label)
	assert.Equal(t, 300.0, out.Data.RevenueTrend[4].Value, "won at 23:30 on the last day is last month")

	require.Len(t, out.Data.ForecastTrend, 6)
	assert.Equal(t, 0.0, out.Data.ForecastTrend[0].Value, "not this month")
	assert.Equal(t, nextMonth.Format("Jan"), out.Data.ForecastTrend[1].Label)
	assert.Equal(t, 1000.0, out.Data.ForecastTrend[1].Value, "a JS Date for the 1st is next month")

	annual := out.Data.AnnualRevenueTrend
	require.NotEmpty(t, annual)
	assert.Equal(t, monthStart.Format("Jan"), annual[len(annual)-1].Label)
	if monthStart.Month() == time.January {
		assert.Equal(t, 700.0, annual[len(annual)-1].Actual, "December's Deal is last year")
	} else {
		assert.Equal(t, 1000.0, annual[len(annual)-1].Actual)
		assert.Equal(t, 300.0, annual[len(annual)-2].Actual, "cumulative through last month")
	}
}

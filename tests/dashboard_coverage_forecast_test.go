package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

type coverageForecastSummary struct {
	Data struct {
		OpenPipelineValue     float64 `json:"open_pipeline_value"`
		PipelineCoverageRatio float64 `json:"pipeline_coverage_ratio"`
		QuarterPipelineValue  float64 `json:"quarter_pipeline_value"`
		OverduePipelineValue  float64 `json:"overdue_pipeline_value"`
		OverduePipelineCount  int64   `json:"overdue_pipeline_count"`
		UndatedPipelineValue  float64 `json:"undated_pipeline_value"`
		UndatedPipelineCount  int64   `json:"undated_pipeline_count"`
		QuarterlySalesTarget  float64 `json:"quarterly_sales_target"`
		ForecastTrend         []struct {
			Label   string  `json:"label"`
			Value   float64 `json:"value"`
			Overdue float64 `json:"overdue"`
		} `json:"forecast_trend"`
	} `json:"data"`
}

// seedCloseDateDeal seeds a Deal with the given status, value, probability
// and expected_close_date (nil for none), written straight to the columns so
// no save hook re-derives them.
func seedCloseDateDeal(t *testing.T, db *gorm.DB, assignedTo *uint, status models.DealStatus, value float64, probability int, closeDate *string) *models.Deal {
	t.Helper()
	deal := seedDeal(t, db, assignedTo)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", deal.ID).UpdateColumns(map[string]interface{}{
		"status":              status,
		"value":               value,
		"probability":         probability,
		"expected_close_date": closeDate,
	}).Error)
	return deal
}

func dayStr(d time.Time) *string {
	s := d.Format("2006-01-02")
	return &s
}

func summaryFor(t *testing.T, app *fiber.App, query string, user *models.User) coverageForecastSummary {
	t.Helper()
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary?"+query, nil, user.ID, user.Role)
	var out coverageForecastSummary
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return out
}

// TestDashboardCoverage_CountsThisQuarterByExpectedCloseDate guards
// pipeline_coverage_ratio counting only open Deals expected to close in the
// current (server-local) quarter, with overdue and undated Deals reported
// separately instead of inflating coverage or vanishing.
func TestDashboardCoverage_CountsThisQuarterByExpectedCloseDate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	today := calendar.Today(time.Now())
	quarterStart := calendar.QuarterStart(today)
	quarter := (int(quarterStart.Month())-1)/3 + 1
	// sales_targets isn't truncated between tests, so the current quarter's
	// row is removed again afterwards (TestSalesTargetCreate_... creates it).
	target := &models.SalesTarget{Year: quarterStart.Year(), Quarter: quarter, TargetValue: 1000000}
	require.NoError(t, db.Create(target).Error)
	t.Cleanup(func() { db.Delete(target) })

	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 100000, 50, dayStr(today))
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 200000, 50, dayStr(quarterStart.AddDate(0, 3, 5)))  // next quarter
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 40000, 50, dayStr(quarterStart.AddDate(0, 0, -10))) // last quarter, overdue
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 7000, 50, nil)
	malformed := "soon"
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 3000, 50, &malformed)
	seedCloseDateDeal(t, db, nil, models.DealStatusWon, 999, 100, dayStr(today)) // not open
	// A JS Date for local midnight on the quarter's last day is still this quarter.
	lastDay := quarterStart.AddDate(0, 3, -1)
	jsDate := time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day(), 0, 0, 0, 0, time.Local).UTC().Format("2006-01-02T15:04:05.000Z")
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 5000, 50, &jsDate)

	wantQuarter, wantOverdue, wantOverdueCount := 105000.0, 40000.0, int64(1)
	if today.After(quarterStart) {
		// Expected earlier this quarter: still this quarter's pipeline, and overdue.
		seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 10000, 50, dayStr(quarterStart))
		wantQuarter, wantOverdue, wantOverdueCount = 115000, 50000, 2
	}
	// quarter + next quarter + last quarter + undated; the in-quarter overdue
	// Deal is already in wantQuarter.
	wantOpen := wantQuarter + 200000 + 40000 + 10000

	out := summaryFor(t, app, "", admin)
	assert.Equal(t, 1000000.0, out.Data.QuarterlySalesTarget)
	assert.Equal(t, wantQuarter, out.Data.QuarterPipelineValue)
	assert.InDelta(t, wantQuarter/1000000, out.Data.PipelineCoverageRatio, 1e-9)
	assert.Equal(t, wantOverdue, out.Data.OverduePipelineValue)
	assert.Equal(t, wantOverdueCount, out.Data.OverduePipelineCount)
	assert.Equal(t, 10000.0, out.Data.UndatedPipelineValue, "no date and an unreadable date")
	assert.Equal(t, int64(2), out.Data.UndatedPipelineCount)
	assert.Equal(t, wantOpen, out.Data.OpenPipelineValue, "open_pipeline_value still counts every open Deal")
}

// TestDashboardForecastTrend_OverdueLandsInCurrentMonth guards overdue open
// Deals staying in forecast_trend (current month, also in its overdue)
// rather than dropping out because their close date passed.
func TestDashboardForecastTrend_OverdueLandsInCurrentMonth(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	today := calendar.Today(time.Now())
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 1000, 50, dayStr(today.AddDate(0, 0, -40))) // overdue
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 400, 50, dayStr(today))                     // due today, not overdue
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 2000, 50, dayStr(monthStart.AddDate(0, 1, 14)))
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 8000, 50, dayStr(monthStart.AddDate(0, 7, 0))) // past the 6 months
	seedCloseDateDeal(t, db, nil, models.DealStatusOpen, 6000, 50, nil)

	out := summaryFor(t, app, "", admin)
	require.Len(t, out.Data.ForecastTrend, 6)
	assert.Equal(t, monthStart.Format("Jan"), out.Data.ForecastTrend[0].Label)
	assert.Equal(t, 700.0, out.Data.ForecastTrend[0].Value, "500 overdue + 200 due today")
	assert.Equal(t, 500.0, out.Data.ForecastTrend[0].Overdue)
	assert.Equal(t, 1000.0, out.Data.ForecastTrend[1].Value)
	total := 0.0
	for i, p := range out.Data.ForecastTrend {
		total += p.Value
		if i > 0 {
			assert.Zero(t, p.Overdue, "overdue only on the current month")
		}
	}
	assert.Equal(t, 1700.0, total, "undated and beyond-6-months Deals aren't in the trend")
}

// TestDashboardCloseDateFigures_ApplyDashboardFilters guards coverage,
// overdue, undated and forecast_trend honoring the filter bar's non-date
// filters (forecast_trend used to ignore them all), but not the created_at
// window, which would drop an old Deal that's due this quarter.
func TestDashboardCloseDateFigures_ApplyDashboardFilters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	repA := testutil.CreateUser(t, db, models.RoleSalesRep)
	repB := testutil.CreateUser(t, db, models.RoleSalesRep)

	today := calendar.Today(time.Now())
	overdue := dayStr(today.AddDate(0, 0, -40))
	product, project := models.BusinessUnitProduct, models.BusinessUnitProject
	// repB's Deals are in another business unit and should never be counted.
	seed := func(rep *models.User, bu models.BusinessUnit, value float64, closeDate *string) *models.Deal {
		deal := seedCloseDateDeal(t, db, &rep.ID, models.DealStatusOpen, value, 100, closeDate)
		require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", deal.ID).UpdateColumn("business_unit", bu).Error)
		return deal
	}
	old := seed(repA, product, 1000, dayStr(today))
	seed(repA, product, 300, overdue)
	seed(repA, product, 50, nil)
	seed(repB, project, 9000, dayStr(today))
	seed(repB, project, 9000, overdue)
	seed(repB, project, 9000, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", old.ID).
		UpdateColumn("created_at", time.Now().AddDate(-2, 0, 0)).Error)

	for _, q := range []string{
		"assigned_to=" + itoa(repA.ID),
		"business_unit=" + string(product),
		"business_unit=" + string(product) + "&period=month",
	} {
		out := summaryFor(t, app, q, admin)
		assert.Equal(t, 1000.0, out.Data.QuarterPipelineValue, q)
		assert.Equal(t, 300.0, out.Data.OverduePipelineValue, q)
		assert.Equal(t, int64(1), out.Data.OverduePipelineCount, q)
		assert.Equal(t, 50.0, out.Data.UndatedPipelineValue, q)
		assert.Equal(t, int64(1), out.Data.UndatedPipelineCount, q)
		require.Len(t, out.Data.ForecastTrend, 6, q)
		assert.Equal(t, 1300.0, out.Data.ForecastTrend[0].Value, q)
		assert.Equal(t, 300.0, out.Data.ForecastTrend[0].Overdue, q)
	}
}

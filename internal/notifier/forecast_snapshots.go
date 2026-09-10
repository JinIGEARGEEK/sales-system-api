// forecast_snapshots.go — the background job feeding "forecast accuracy"
// history (models.ForecastSnapshot). Runs on its own ticker, same
// separate-goroutine reasoning as workflow_rules.go: a bug here shouldn't be
// able to stall task reminders or notification rules.
package notifier

import (
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const forecastSnapshotInterval = 24 * time.Hour

// StartForecastSnapshots launches a background goroutine that captures one
// ForecastSnapshot per day for the current (Year, Quarter). Idempotent across
// restarts — takeForecastSnapshot skips the day if a snapshot already exists
// for today's date, so a redeploy mid-day never produces a duplicate row.
func StartForecastSnapshots(db *gorm.DB, cfg *config.Config) {
	ticker := time.NewTicker(forecastSnapshotInterval)
	go func() {
		takeForecastSnapshot(db)
		for range ticker.C {
			takeForecastSnapshot(db)
		}
	}()
}

func takeForecastSnapshot(db *gorm.DB) {
	today := time.Now().Truncate(24 * time.Hour)

	var existing int64
	db.Model(&models.ForecastSnapshot{}).Where("snapshot_date = ?", today).Count(&existing)
	if existing > 0 {
		return
	}

	year, quarter := today.Year(), (int(today.Month())-1)/3+1

	// Same category split as dashboard.go's forecastByCategory, but scoped to
	// Deals whose expected_close_date falls within this (Year, Quarter) —
	// the dashboard figure is "all open pipeline, whenever it closes", while a
	// snapshot needs "what's expected to close THIS quarter" to be
	// comparable against ActualWonToDate below.
	rangeStart, rangeEnd := quarterDateRange(year, quarter)

	var rows []struct {
		Category string
		Value    float64
	}
	db.Model(&models.Deal{}).
		Where("status = ? AND expected_close_date >= ? AND expected_close_date < ?",
			models.DealStatusOpen, rangeStart.Format("2006-01-02"), rangeEnd.Format("2006-01-02")).
		Select("COALESCE(forecast_category, 'Pipeline') as category, " +
			"COALESCE(SUM(value * COALESCE(probability, 0) / 100.0), 0) as value").
		Group("category").Scan(&rows)

	snapshot := models.ForecastSnapshot{SnapshotDate: today, Year: year, Quarter: quarter}
	for _, r := range rows {
		switch models.ForecastCategory(r.Category) {
		case models.ForecastCategoryCommit:
			snapshot.CommitValue = r.Value
		case models.ForecastCategoryBestCase:
			snapshot.BestCaseValue = r.Value
		default:
			snapshot.PipelineValue += r.Value
		}
	}
	snapshot.WeightedForecast = snapshot.CommitValue + snapshot.BestCaseValue + snapshot.PipelineValue
	snapshot.SalesTarget = quarterlyTarget(db, year, quarter)

	var actualWon float64
	db.Model(&models.Deal{}).
		Where("status = ? AND expected_close_date >= ? AND expected_close_date < ?",
			models.DealStatusWon, rangeStart.Format("2006-01-02"), rangeEnd.Format("2006-01-02")).
		Select("COALESCE(SUM(value), 0)").Scan(&actualWon)
	snapshot.ActualWonToDate = actualWon

	if err := db.Create(&snapshot).Error; err != nil {
		log.Printf("notifier: failed to create forecast snapshot: %v", err)
	}
}

// quarterDateRange returns [start, end) calendar bounds for (year, quarter).
func quarterDateRange(year, quarter int) (time.Time, time.Time) {
	startMonth := time.Month((quarter-1)*3 + 1)
	start := time.Date(year, startMonth, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 3, 0)
	return start, end
}

// quarterlyTarget resolves (year, quarter)'s SalesTarget row, falling back to
// AppSettings.QuarterlySalesTarget/4 — duplicated from dashboard.go's
// currentQuarterTarget (for any period, not just "right now") rather than
// imported, keeping this package self-contained the same way
// workflow_rules.go's checkers already do.
func quarterlyTarget(db *gorm.DB, year, quarter int) float64 {
	var target models.SalesTarget
	if err := db.Where("year = ? AND quarter = ?", year, quarter).First(&target).Error; err == nil {
		return float64(target.TargetValue)
	}
	settings := utils.GetAppSettings(db)
	return float64(settings.QuarterlySalesTarget) / 4
}

// forecast_snapshots.go — the background job feeding "forecast accuracy"
// history (models.ForecastSnapshot). Runs on its own ticker, same
// separate-goroutine reasoning as workflow_rules.go: a bug here shouldn't be
// able to stall task reminders or notification rules.
package notifier

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// forecastSnapshotInterval is how often the job checks whether today's
// snapshot exists yet — hourly rather than daily, since the check is one
// indexed count: a pass that failed (DB blip) is retried within the hour
// instead of losing the whole day, and the first pass of a new local day
// happens soon after midnight rather than at whatever time of day the
// process happened to boot.
const forecastSnapshotInterval = time.Hour

// StartForecastSnapshots launches a background goroutine that captures one
// ForecastSnapshot per day for the current (Year, Quarter). Idempotent across
// restarts and replicas — takeForecastSnapshot skips the day if a snapshot
// already exists for today's date, and the insert yields to a concurrent
// one on the snapshot_date unique index. Stops when ctx is cancelled (see
// runEvery).
func StartForecastSnapshots(ctx context.Context, db *gorm.DB, cfg *config.Config) {
	runEvery(ctx, "forecast snapshots", forecastSnapshotInterval, func() { takeForecastSnapshot(db, time.Now()) })
}

// takeForecastSnapshot writes now's day's snapshot unless one exists. Any
// query failure skips the day's row entirely (retried next tick) rather than
// writing zeros, which the unique snapshot_date would keep for the day.
func takeForecastSnapshot(db *gorm.DB, now time.Time) {
	// The server-local calendar day, not UTC's.
	today := calendar.Today(now)

	var existing int64
	if err := db.Model(&models.ForecastSnapshot{}).Where("snapshot_date = ?", today).Count(&existing).Error; err != nil {
		log.Printf("notifier: forecast snapshot: check existing: %v", err)
		return
	}
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
	if err := db.Model(&models.Deal{}).
		Where("status = ? AND expected_close_date >= ? AND expected_close_date < ?",
			models.DealStatusOpen, rangeStart.Format("2006-01-02"), rangeEnd.Format("2006-01-02")).
		Select("COALESCE(forecast_category, 'Pipeline') as category, " +
			"COALESCE(SUM(value * COALESCE(probability, 0) / 100.0), 0) as value").
		Group("category").Scan(&rows).Error; err != nil {
		log.Printf("notifier: forecast snapshot: open pipeline: %v", err)
		return
	}

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
	if err := db.Model(&models.Deal{}).
		Where("status = ? AND expected_close_date >= ? AND expected_close_date < ?",
			models.DealStatusWon, rangeStart.Format("2006-01-02"), rangeEnd.Format("2006-01-02")).
		Select("COALESCE(SUM(value), 0)").Scan(&actualWon).Error; err != nil {
		log.Printf("notifier: forecast snapshot: won to date: %v", err)
		return
	}
	snapshot.ActualWonToDate = actualWon

	// DO NOTHING on the snapshot_date unique index: with several replicas
	// each one's pass can get past the count above at the same moment, and
	// the loser's row would be identical anyway.
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; err != nil {
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

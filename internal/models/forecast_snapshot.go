package models

import "time"

// ForecastSnapshot is a daily point-in-time capture of the weighted forecast
// for one (Year, Quarter) period — the raw material for a "forecast accuracy"
// history: once a quarter closes, ActualWonToDate on its final snapshots can
// be compared against WeightedForecast/the per-category splits to see how
// close the forecast actually came. Populated by
// internal/notifier/forecast_snapshots.go's daily background job, never
// written by any handler directly.
type ForecastSnapshot struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	SnapshotDate time.Time `gorm:"type:date;not null;uniqueIndex:idx_forecast_snapshot_date" json:"snapshot_date"`
	Year         int       `gorm:"not null;index:idx_forecast_snapshot_period" json:"year"`
	Quarter      int       `gorm:"not null;index:idx_forecast_snapshot_period" json:"quarter"`
	// Commit/BestCase/Pipeline are the same probability-weighted (value ×
	// probability/100) split as dashboard.go's forecastByCategory, restricted
	// to open Deals whose expected_close_date falls in (Year, Quarter).
	CommitValue   float64 `json:"commit_value"`
	BestCaseValue float64 `json:"best_case_value"`
	PipelineValue float64 `json:"pipeline_value"`
	// WeightedForecast is Commit+BestCase+Pipeline, kept as its own column
	// rather than computed on read so a snapshot row is a fully self-contained
	// historical record even if the split formula changes later.
	WeightedForecast float64 `json:"weighted_forecast"`
	// SalesTarget is this (Year, Quarter)'s target at snapshot time — see
	// SalesTarget.TargetValue, falling back to AppSettings.QuarterlySalesTarget/4
	// the same way dashboard.go's currentQuarterTarget does.
	SalesTarget float64 `json:"sales_target"`
	// ActualWonToDate is the sum of Won Deal value with expected_close_date in
	// (Year, Quarter), as of SnapshotDate — this grows across the quarter and
	// its value on the last snapshot before the quarter ends is the "actual"
	// side of the forecast-vs-actual comparison.
	ActualWonToDate float64   `json:"actual_won_to_date"`
	CreatedAt       time.Time `json:"created_at"`
}

func (ForecastSnapshot) TableName() string { return "forecast_snapshots" }

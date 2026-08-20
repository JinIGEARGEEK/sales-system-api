package models

import "time"

// SalesTarget is an Admin-configurable quarterly sales target for one
// specific (Year, Quarter) period — FR-CRM-092. Unlike AppSettings'
// QuarterlySalesTarget (a single flat scalar always treated as "whatever
// quarter it is right now"), this is a row-per-period table: an Admin can
// pre-set Q4 2026's target while Q3 2026 is still active, or set every
// quarter of 2027 in advance, without waiting for the period to start.
//
// TargetValue is the true quarterly figure — NOT divided by 4 the way
// AppSettings.QuarterlySalesTarget is derived-from-annual in dashboard.go's
// pipeline_coverage_ratio calc. Any quarter with no SalesTarget row falls
// back to AppSettings.QuarterlySalesTarget/4 (dashboard.go's
// currentQuarterTarget) so this feature is purely additive — nothing
// breaks for an Admin who never uses it.
type SalesTarget struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Year        int       `gorm:"not null;uniqueIndex:idx_sales_target_period" json:"year"`
	Quarter     int       `gorm:"not null;uniqueIndex:idx_sales_target_period" json:"quarter"`
	TargetValue int64     `gorm:"not null" json:"target_value"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   *uint     `json:"created_by,omitempty"`
	UpdatedBy   *uint     `json:"updated_by,omitempty"`
}

func (SalesTarget) TableName() string { return "sales_targets" }

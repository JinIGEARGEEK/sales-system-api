package models

// AppSettings is a singleton row (always ID=1) holding Admin-configurable,
// app-wide settings that don't warrant their own table — starting with the
// quarterly sales quota used by dashboard.go's pipeline_coverage_ratio
// (previously the hardcoded `quarterlySalesTarget` const, FR-CRM-058).
// Unlike PipelineStage/LeadSourceOption (row-per-option config), this is a
// key-value-style singleton: exactly one row ever exists, seeded on first run
// the same way seedAdmin/seedPipelineConfig seed their tables in main.go.
type AppSettings struct {
	ID                   uint  `gorm:"primaryKey" json:"id"`
	QuarterlySalesTarget int64 `gorm:"not null;default:3000000" json:"quarterly_sales_target"`
}

func (AppSettings) TableName() string { return "app_settings" }

// DefaultAppSettings is the seed row inserted on first run if app_settings is
// empty — mirrors DefaultPipelineStages/DefaultLeadSourceOptions.
var DefaultAppSettings = AppSettings{
	ID:                   1,
	QuarterlySalesTarget: 3000000,
}

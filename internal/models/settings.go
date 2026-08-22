package models

import "time"

// AppSettings is a singleton row (always ID=1) holding Admin-configurable,
// app-wide settings that don't warrant their own table — starting with the
// quarterly sales quota used by dashboard.go's pipeline_coverage_ratio
// (previously the hardcoded `quarterlySalesTarget` const, FR-CRM-058), and
// now also the annual revenue goal used by dashboard.go's
// annual_revenue_progress_ratio (FR-CRM-091). Unlike PipelineStage/
// LeadSourceOption (row-per-option config), this is a key-value-style
// singleton: exactly one row ever exists, seeded on first run the same way
// seedAdmin/seedPipelineConfig seed their tables in main.go.
//
// UpdatedAt is GORM's usual auto-managed-by-field-name column (no tag
// needed) — surfaced in the API response so the Admin config UI can show
// "last updated" next to the quota/goal fields. Since neither figure resets
// itself automatically (the annual goal in particular has no per-year
// value — see FR-CRM-091's note on manual year rollover), a visible
// staleness hint is the cheapest guard against a stale number quietly
// surviving into a new year unnoticed.
type AppSettings struct {
	ID                   uint  `gorm:"primaryKey" json:"id"`
	QuarterlySalesTarget int64 `gorm:"not null;default:3000000" json:"quarterly_sales_target"`
	AnnualRevenueGoal    int64 `gorm:"not null;default:12000000" json:"annual_revenue_goal"`
	// LeadScoringMqlThreshold — FR-CRM-007: a Lead whose computed Score meets
	// or exceeds this value is classified "mql". Lives here rather than a new
	// singleton table since it's one more Admin-tunable number, same shape as
	// the two fields above.
	LeadScoringMqlThreshold int `gorm:"not null;default:50" json:"lead_scoring_mql_threshold"`
	// RequireSignedContractBeforeWon — FR-CRM-045: "configurable, not
	// hard-enforced by default," so it defaults false. When true,
	// DealHandler blocks a Deal from moving into Won (via Create, Update, or
	// UpdateStage) unless it already has at least one Contract with
	// status Signed.
	RequireSignedContractBeforeWon bool      `gorm:"not null;default:false" json:"require_signed_contract_before_won"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

func (AppSettings) TableName() string { return "app_settings" }

// DefaultAppSettings is the seed row inserted on first run if app_settings is
// empty — mirrors DefaultPipelineStages/DefaultLeadSourceOptions.
var DefaultAppSettings = AppSettings{
	ID:                             1,
	QuarterlySalesTarget:           3000000,
	AnnualRevenueGoal:              12000000,
	LeadScoringMqlThreshold:        50,
	RequireSignedContractBeforeWon: false,
}

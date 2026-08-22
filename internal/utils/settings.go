package utils

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// GetAppSettings loads the AppSettings singleton row (id: 1), falling back to
// models.DefaultAppSettings rather than erroring if it's somehow missing
// (e.g. the seed hasn't run yet). Shared by every handler that needs a
// read-only look at the settings — SettingsHandler.Get itself, DashboardHandler
// (quarterly_sales_target/annual_revenue_goal), and DealHandler's
// require_signed_contract_before_won (FR-CRM-045) check — so the fallback
// behavior only needs to change in one place.
func GetAppSettings(db *gorm.DB) models.AppSettings {
	var settings models.AppSettings
	if err := db.First(&settings, 1).Error; err != nil {
		return models.DefaultAppSettings
	}
	return settings
}

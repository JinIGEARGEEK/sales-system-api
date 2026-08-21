package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// SettingsHandler — Admin-only read/update of the AppSettings singleton row
// (FR-CRM-058: quarterly sales quota; FR-CRM-091: annual revenue goal —
// both previously hardcoded in dashboard.go).
type SettingsHandler struct {
	DB *gorm.DB
}

func NewSettingsHandler(db *gorm.DB) *SettingsHandler {
	return &SettingsHandler{DB: db}
}

// get loads the singleton row (ID=1), falling back to DefaultAppSettings if
// it's somehow missing (e.g. seed hasn't run yet) rather than erroring.
func (h *SettingsHandler) get() (models.AppSettings, error) {
	var settings models.AppSettings
	if err := h.DB.First(&settings, 1).Error; err != nil {
		return models.DefaultAppSettings, err
	}
	return settings, nil
}

// Get — GET /admin/settings.
func (h *SettingsHandler) Get(c *fiber.Ctx) error {
	settings, err := h.get()
	if err != nil {
		return utils.Internal(c, "Failed to load settings")
	}
	return utils.OK(c, settings)
}

type settingsForm struct {
	QuarterlySalesTarget    *int64 `json:"quarterly_sales_target"`
	AnnualRevenueGoal       *int64 `json:"annual_revenue_goal"`
	LeadScoringMqlThreshold *int64 `json:"lead_scoring_mql_threshold"`
}

// requireNonNegative validates one required *int64 form field, writing the
// 422 response itself and returning false if it's missing or negative — the
// same two checks every field on settingsForm needs, factored out so adding
// a third Admin-configurable figure later doesn't mean copy-pasting this
// pair of checks again. Returns a bool rather than propagating
// utils.ValidationError's own return value as an error: like the other
// standalone (non-handler) validation helpers documented on utils.ErrHandled,
// ValidationError's return is nil on the successful write of the 422 body
// itself — forwarding that as "no error" would make an `if err != nil`
// caller silently fall through past a failed validation.
func requireNonNegative(c *fiber.Ctx, field string, value *int64) bool {
	if value == nil {
		_ = utils.ValidationError(c, field+" is required", map[string][]string{field: {"required"}})
		return false
	}
	if *value < 0 {
		_ = utils.ValidationError(c, field+" must be non-negative", map[string][]string{field: {"must be >= 0"}})
		return false
	}
	return true
}

// Update — PATCH /admin/settings. Both fields are required on every PATCH
// (this is a single singleton row, not a per-field partial-update resource —
// same convention as the original quarterly_sales_target-only form).
func (h *SettingsHandler) Update(c *fiber.Ctx) error {
	settings, err := h.get()
	if err != nil {
		return utils.Internal(c, "Failed to load settings")
	}

	var form settingsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !requireNonNegative(c, "quarterly_sales_target", form.QuarterlySalesTarget) {
		return nil
	}
	if !requireNonNegative(c, "annual_revenue_goal", form.AnnualRevenueGoal) {
		return nil
	}
	// Unlike the two fields above, lead_scoring_mql_threshold is optional on
	// PATCH: it was added after quarterly_sales_target/annual_revenue_goal
	// were already an established "both required" pair, and existing clients
	// (and this handler's own pre-existing tests) PATCH those two without
	// knowing this field exists. Omitting it just leaves the current value
	// in place instead of 422ing every settings PATCH that predates it.
	if form.LeadScoringMqlThreshold != nil && *form.LeadScoringMqlThreshold < 0 {
		return utils.ValidationError(c, "lead_scoring_mql_threshold must be non-negative", map[string][]string{"lead_scoring_mql_threshold": {"must be >= 0"}})
	}

	oldQuarterlyTarget, oldAnnualGoal, oldMqlThreshold := settings.QuarterlySalesTarget, settings.AnnualRevenueGoal, settings.LeadScoringMqlThreshold
	before := models.JSONMap{"quarterly_sales_target": oldQuarterlyTarget, "annual_revenue_goal": oldAnnualGoal, "lead_scoring_mql_threshold": oldMqlThreshold}

	settings.QuarterlySalesTarget = *form.QuarterlySalesTarget
	settings.AnnualRevenueGoal = *form.AnnualRevenueGoal
	if form.LeadScoringMqlThreshold != nil {
		settings.LeadScoringMqlThreshold = int(*form.LeadScoringMqlThreshold)
	}
	after := models.JSONMap{"quarterly_sales_target": settings.QuarterlySalesTarget, "annual_revenue_goal": settings.AnnualRevenueGoal, "lead_scoring_mql_threshold": settings.LeadScoringMqlThreshold}

	changed := oldQuarterlyTarget != settings.QuarterlySalesTarget || oldAnnualGoal != settings.AnnualRevenueGoal || oldMqlThreshold != settings.LeadScoringMqlThreshold
	err = utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Save(&settings).Error },
		changed, "settings", settings.ID, "updated", before, after, middleware.CurrentUserID(c))
	if err != nil {
		return utils.Internal(c, "Failed to update settings")
	}
	// Both fields feed straight into GET /dashboard/summary's response
	// (pipeline_coverage_ratio, annual_revenue_goal/annual_revenue_progress_ratio)
	// but a settings PATCH never touches the deals table, so nothing else
	// would ever invalidate that response cache for a real change here —
	// without this, the Admin who just changed the goal would see the old
	// value on their own next dashboard load for up to the cache's TTL.
	if changed {
		InvalidateDashboardCache()
	}
	return utils.OK(c, settings)
}

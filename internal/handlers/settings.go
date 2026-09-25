package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/digest"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// SettingsHandler — Admin-only read/update of the AppSettings singleton row
// (FR-CRM-058: quarterly sales quota; FR-CRM-091: annual revenue goal —
// both previously hardcoded in dashboard.go).
type SettingsHandler struct {
	DB  *gorm.DB
	cfg *config.Config
}

func NewSettingsHandler(db *gorm.DB, cfg *config.Config) *SettingsHandler {
	return &SettingsHandler{DB: db, cfg: cfg}
}

// Get godoc
// @Summary Get app settings
// @Description Admin-only. Returns the AppSettings singleton row (quarterly sales quota, annual revenue goal, and related app-wide config).
// @Tags admin/settings
// @Security BearerAuth
// @Produce json
// @Success 200 {object} models.AppSettings
// @Router /admin/settings [get]
func (h *SettingsHandler) Get(c *fiber.Ctx) error {
	settings := utils.GetAppSettings(h.DB)
	settings.SMTPConfigured = h.cfg.SMTPHost != ""
	return utils.OK(c, settings)
}

type settingsForm struct {
	QuarterlySalesTarget           *int64 `json:"quarterly_sales_target"`
	AnnualRevenueGoal              *int64 `json:"annual_revenue_goal"`
	LeadScoringMqlThreshold        *int64 `json:"lead_scoring_mql_threshold"`
	RequireSignedContractBeforeWon *bool  `json:"require_signed_contract_before_won"`
	WeeklyDigestEnabled            *bool  `json:"weekly_digest_enabled"`
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

// Update godoc
// @Summary Update app settings
// @Description Admin-only. quarterly_sales_target and annual_revenue_goal are required on every PATCH (this is a singleton row, not a per-field partial-update resource); both must be non-negative. lead_scoring_mql_threshold, require_signed_contract_before_won and weekly_digest_enabled are optional and left unchanged if omitted.
// @Tags admin/settings
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body settingsForm true "Settings fields"
// @Success 200 {object} models.AppSettings
// @Failure 400 {object} map[string]interface{}
// @Router /admin/settings [patch]
func (h *SettingsHandler) Update(c *fiber.Ctx) error {
	settings := utils.GetAppSettings(h.DB)

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
	oldRequireSignedContract := settings.RequireSignedContractBeforeWon
	oldWeeklyDigest := settings.WeeklyDigestEnabled
	before := models.JSONMap{
		"quarterly_sales_target": oldQuarterlyTarget, "annual_revenue_goal": oldAnnualGoal,
		"lead_scoring_mql_threshold": oldMqlThreshold, "require_signed_contract_before_won": oldRequireSignedContract,
		"weekly_digest_enabled": oldWeeklyDigest,
	}

	settings.QuarterlySalesTarget = *form.QuarterlySalesTarget
	settings.AnnualRevenueGoal = *form.AnnualRevenueGoal
	if form.LeadScoringMqlThreshold != nil {
		settings.LeadScoringMqlThreshold = int(*form.LeadScoringMqlThreshold)
	}
	// Optional on PATCH for the same reason lead_scoring_mql_threshold is —
	// added after quarterly_sales_target/annual_revenue_goal were already an
	// established "both required" pair.
	if form.RequireSignedContractBeforeWon != nil {
		settings.RequireSignedContractBeforeWon = *form.RequireSignedContractBeforeWon
	}
	// Optional, same as the flag above.
	if form.WeeklyDigestEnabled != nil {
		settings.WeeklyDigestEnabled = *form.WeeklyDigestEnabled
	}
	after := models.JSONMap{
		"quarterly_sales_target": settings.QuarterlySalesTarget, "annual_revenue_goal": settings.AnnualRevenueGoal,
		"lead_scoring_mql_threshold": settings.LeadScoringMqlThreshold, "require_signed_contract_before_won": settings.RequireSignedContractBeforeWon,
		"weekly_digest_enabled": settings.WeeklyDigestEnabled,
	}

	changed := oldQuarterlyTarget != settings.QuarterlySalesTarget || oldAnnualGoal != settings.AnnualRevenueGoal ||
		oldMqlThreshold != settings.LeadScoringMqlThreshold || oldRequireSignedContract != settings.RequireSignedContractBeforeWon ||
		oldWeeklyDigest != settings.WeeklyDigestEnabled
	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Omit("last_weekly_digest_at").Save(&settings).Error },
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
	settings.SMTPConfigured = h.cfg.SMTPHost != ""
	return utils.OK(c, settings)
}

// weeklyDigestPreview is what GET /admin/weekly-digest/preview returns: the
// digest as it would go out now, plus whether it actually can.
type weeklyDigestPreview struct {
	Subject        string     `json:"subject"`
	Body           string     `json:"body"`
	Recipients     []string   `json:"recipients"`
	WeekFrom       string     `json:"week_from"`
	WeekTo         string     `json:"week_to"`
	Enabled        bool       `json:"enabled"`
	SMTPConfigured bool       `json:"smtp_configured"`
	LastSentAt     *time.Time `json:"last_sent_at"`
}

// WeeklyDigestPreview — GET /admin/weekly-digest/preview (Admin). Renders
// last week's digest without sending it, so an Admin can see exactly what
// Admins/Sales Managers get each Monday.
func (h *SettingsHandler) WeeklyDigestPreview(c *fiber.Ctx) error {
	weekly, err := digest.BuildWeekly(h.DB, h.cfg, time.Now())
	if err != nil {
		return utils.Internal(c, "Failed to build weekly digest")
	}
	settings := utils.GetAppSettings(h.DB)
	return utils.OK(c, weeklyDigestPreview{
		Subject: weekly.Subject, Body: weekly.Body, Recipients: weekly.Recipients,
		WeekFrom: weekly.Week.From.Format("2006-01-02"), WeekTo: weekly.Week.To.AddDate(0, 0, -1).Format("2006-01-02"),
		Enabled: settings.WeeklyDigestEnabled, SMTPConfigured: h.cfg != nil && h.cfg.SMTPHost != "",
		LastSentAt: settings.LastWeeklyDigestAt,
	})
}

// SendWeeklyDigestTest — POST /admin/weekly-digest/test (Admin). Emails the
// current digest to the calling Admin only, so they can check it arrives and
// reads well; doesn't touch the Monday schedule.
func (h *SettingsHandler) SendWeeklyDigestTest(c *fiber.Ctx) error {
	if h.cfg == nil || h.cfg.SMTPHost == "" {
		return utils.ValidationError(c, "Email isn't configured on the server (SMTP_HOST)", map[string][]string{"smtp": {"not_configured"}})
	}
	var me models.User
	if err := h.DB.First(&me, middleware.CurrentUserID(c)).Error; err != nil || me.Email == "" {
		return utils.ValidationError(c, "Your account has no email address", map[string][]string{"email": {"missing"}})
	}
	weekly, err := digest.BuildWeekly(h.DB, h.cfg, time.Now())
	if err != nil {
		return utils.Internal(c, "Failed to build weekly digest")
	}
	if err := utils.SendMail(h.cfg, me.Email, "[Test] "+weekly.Subject, weekly.Body); err != nil {
		return utils.Internal(c, "Failed to send the test email")
	}
	return utils.OK(c, fiber.Map{"sent_to": me.Email})
}

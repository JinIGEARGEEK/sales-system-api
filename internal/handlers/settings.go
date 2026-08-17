package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// SettingsHandler — Admin-only read/update of the AppSettings singleton row
// (FR-CRM-058: quarterly sales quota, previously hardcoded in dashboard.go).
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
	QuarterlySalesTarget *int64 `json:"quarterly_sales_target"`
}

// Update — PATCH /admin/settings.
func (h *SettingsHandler) Update(c *fiber.Ctx) error {
	settings, err := h.get()
	if err != nil {
		return utils.Internal(c, "Failed to load settings")
	}

	var form settingsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.QuarterlySalesTarget == nil {
		return utils.ValidationError(c, "quarterly_sales_target is required", map[string][]string{"quarterly_sales_target": {"required"}})
	}
	if *form.QuarterlySalesTarget < 0 {
		return utils.ValidationError(c, "quarterly_sales_target must be non-negative", map[string][]string{"quarterly_sales_target": {"must be >= 0"}})
	}

	settings.QuarterlySalesTarget = *form.QuarterlySalesTarget
	if err := h.DB.Save(&settings).Error; err != nil {
		return utils.Internal(c, "Failed to update settings")
	}
	return utils.OK(c, settings)
}

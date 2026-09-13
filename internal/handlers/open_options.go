package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// OpenOptionsHandler exposes the active option lists Company/Contact fields
// validate against (size, revenue_size, industry, role_title) to external
// Open API integrators. Before this, those values were only readable via
// /admin/company-sizes, /admin/revenue-sizes, /admin/job-titles,
// /admin/industries — all gated behind the staff Bearer-JWT login an
// external partner doesn't have, so an integrator's only option was asking
// an Admin out of band and keeping a manually-synced copy (see
// docs/OPEN_API_GUIDE.md). Read-only, gated by the same X-API-Key auth as
// every other /open/* route.
type OpenOptionsHandler struct {
	DB *gorm.DB
}

func NewOpenOptionsHandler(db *gorm.DB) *OpenOptionsHandler {
	return &OpenOptionsHandler{DB: db}
}

type openOptions struct {
	Industries   []string `json:"industries"`
	Sizes        []string `json:"sizes"`
	RevenueSizes []string `json:"revenue_sizes"`
	JobTitles    []string `json:"job_titles"`
}

// List godoc
// @Summary List valid option values (Open API)
// @Description Returns every currently active option value Company/Contact fields validate against: industry (free text, shown here as a reference list rather than an enforced one — any value is still accepted), size, revenue_size, role_title. Lets an external integrator discover valid values without a staff login.
// @Tags open
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} openOptions
// @Router /open/options [get]
func (h *OpenOptionsHandler) List(c *fiber.Ctx) error {
	var out openOptions
	if err := h.DB.Model(&models.IndustryOption{}).Where("is_active = ?", true).
		Order("name ASC").Pluck("name", &out.Industries).Error; err != nil {
		return utils.Internal(c, "Failed to list options")
	}
	if err := h.DB.Model(&models.CompanySizeOption{}).Where("is_active = ?", true).
		Order("name ASC").Pluck("name", &out.Sizes).Error; err != nil {
		return utils.Internal(c, "Failed to list options")
	}
	if err := h.DB.Model(&models.RevenueSizeOption{}).Where("is_active = ?", true).
		Order("name ASC").Pluck("name", &out.RevenueSizes).Error; err != nil {
		return utils.Internal(c, "Failed to list options")
	}
	if err := h.DB.Model(&models.JobTitleOption{}).Where("is_active = ?", true).
		Order("name ASC").Pluck("name", &out.JobTitles).Error; err != nil {
		return utils.Internal(c, "Failed to list options")
	}
	return utils.OK(c, out)
}

package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// dashboard_prospect.go — Marketing's own dashboard tab (FR-CRM-107), added
// 2026-09-03. Deliberately a separate handler/file from dashboard.go's
// Deal-centric Summary — it has its own caching/period-preset machinery this
// doesn't need (Prospect volume doesn't warrant it), and mixing an unrelated
// entity into that already-large method would just make it harder to read.

type prospectStatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type ProspectDashboardSummary struct {
	TotalProspects  int64                      `json:"total_prospects"`
	OpenProspects   int64                      `json:"open_prospects"`
	ConvertedCount  int64                      `json:"converted_count"`
	ConversionRate  float64                    `json:"conversion_rate"`
	StatusBreakdown []prospectStatusCount      `json:"status_breakdown"`
	SourceBreakdown []prospectSourceConversion `json:"source_breakdown"`
}

// prospectSummaryFilter — same date_from/date_to/assigned_to params as
// GET /reports/prospect-source-conversion (fetchProspectSourceConversion,
// reused directly below for SourceBreakdown — same cross-handler-reuse
// pattern dashboard.go's own Summary already uses for
// ReportHandler.fetchSalesCycle). Returns a fresh query each call rather
// than a query cloned/reused across calls, so each aggregate below applies
// its own additional filter (e.g. status) without the others bleeding in.
func (h *DashboardHandler) prospectSummaryFilter(c *fiber.Ctx) *gorm.DB {
	query := h.DB.Model(&models.Prospect{})
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("date_from"); v != "" {
		query = query.Where("created_at >= ?", v)
	}
	if v := c.Query("date_to"); v != "" {
		query = query.Where("created_at <= ?", v)
	}
	return query
}

// ProspectSummary — GET /dashboard/prospect-summary?assigned_to=&date_from=&date_to=
// Not RequireRoles-gated at the route — any authenticated role, same
// openness as GET /dashboard/summary itself; the frontend decides which
// role sees which dashboard tab.
func (h *DashboardHandler) ProspectSummary(c *fiber.Ctx) error {
	var total int64
	h.prospectSummaryFilter(c).Count(&total)

	var converted int64
	h.prospectSummaryFilter(c).Where("status = ?", models.ProspectStatusConverted).Count(&converted)

	var statusRows []prospectStatusCount
	if err := h.prospectSummaryFilter(c).
		Select("status, count(*) as count").
		Group("status").
		Scan(&statusRows).Error; err != nil {
		return utils.Internal(c, "Failed to compute prospect status breakdown")
	}

	var openCount int64
	for _, row := range statusRows {
		if row.Status != string(models.ProspectStatusConverted) && row.Status != string(models.ProspectStatusDisqualified) {
			openCount += row.Count
		}
	}

	rate := 0.0
	if total > 0 {
		rate = float64(converted) / float64(total) * 100
	}

	sourceBreakdown, err := (&ReportHandler{DB: h.DB}).fetchProspectSourceConversion(c)
	if err != nil {
		return utils.Internal(c, "Failed to compute prospect source breakdown")
	}

	return utils.OK(c, ProspectDashboardSummary{
		TotalProspects:  total,
		OpenProspects:   openCount,
		ConvertedCount:  converted,
		ConversionRate:  rate,
		StatusBreakdown: statusRows,
		SourceBreakdown: sourceBreakdown,
	})
}

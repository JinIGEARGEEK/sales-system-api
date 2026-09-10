package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// dashboard_lead.go — Lead stats for the dashboard's Sales tab, mirroring
// dashboard_prospect.go's Marketing-tab pattern exactly (own filter/handler,
// SourceBreakdown reused from the existing lead-source-conversion report).

type leadStatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type LeadDashboardSummary struct {
	TotalLeads        int64                  `json:"total_leads"`
	NewLeads          int64                  `json:"new_leads"`
	QualifiedLeads    int64                  `json:"qualified_leads"`
	DisqualifiedLeads int64                  `json:"disqualified_leads"`
	StatusBreakdown   []leadStatusCount      `json:"status_breakdown"`
	SourceBreakdown   []leadSourceConversion `json:"source_breakdown"`
}

// leadSummaryFilter — same date_from/date_to/assigned_to params as
// GET /reports/lead-source-conversion (fetchLeadSourceConversion, reused
// directly below for SourceBreakdown). Returns a fresh query each call so
// each aggregate below applies its own filter without the others bleeding in.
func (h *DashboardHandler) leadSummaryFilter(c *fiber.Ctx) *gorm.DB {
	query := h.DB.Model(&models.Lead{})
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

// LeadSummary — GET /dashboard/lead-summary?assigned_to=&date_from=&date_to=
// Not RequireRoles-gated at the route — any authenticated role, same
// openness as GET /dashboard/summary and /dashboard/prospect-summary; the
// frontend decides which role sees which dashboard tab.
func (h *DashboardHandler) LeadSummary(c *fiber.Ctx) error {
	var total int64
	h.leadSummaryFilter(c).Count(&total)

	var statusRows []leadStatusCount
	if err := h.leadSummaryFilter(c).
		Select("status, count(*) as count").
		Group("status").
		Scan(&statusRows).Error; err != nil {
		return utils.Internal(c, "Failed to compute lead status breakdown")
	}

	var newCount, qualifiedCount, disqualifiedCount int64
	for _, row := range statusRows {
		switch row.Status {
		case string(models.LeadStatusNew):
			newCount = row.Count
		case string(models.LeadStatusQualified):
			qualifiedCount = row.Count
		case string(models.LeadStatusDisqualified):
			disqualifiedCount = row.Count
		}
	}

	sourceBreakdown, err := (&ReportHandler{DB: h.DB}).fetchLeadSourceConversion(c)
	if err != nil {
		return utils.Internal(c, "Failed to compute lead source breakdown")
	}

	return utils.OK(c, LeadDashboardSummary{
		TotalLeads:        total,
		NewLeads:          newCount,
		QualifiedLeads:    qualifiedCount,
		DisqualifiedLeads: disqualifiedCount,
		StatusBreakdown:   statusRows,
		SourceBreakdown:   sourceBreakdown,
	})
}

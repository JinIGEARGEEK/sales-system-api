package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const quarterlySalesTarget = 3000000 // FR-CRM-058 will make this Admin-configurable

type DashboardHandler struct {
	DB *gorm.DB
}

func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db}
}

// baseFilter applies the shared date_from/date_to (or period), business_unit,
// business_unit_item, channel, assigned_to (Sales Rep), and company_tag query
// params — api-system-spec.md §9, FR-CRM-055.
func (h *DashboardHandler) baseFilter(c *fiber.Ctx) *gorm.DB {
	query := h.DB.Model(&models.Deal{})

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")
	if dateFrom == "" && dateTo == "" {
		if from, ok := periodStart(c.Query("period")); ok {
			query = query.Where("created_at >= ?", from)
		}
	} else {
		if dateFrom != "" {
			query = query.Where("created_at >= ?", dateFrom)
		}
		if dateTo != "" {
			query = query.Where("created_at <= ?", dateTo)
		}
	}

	if v := c.Query("business_unit"); v != "" {
		query = query.Where("business_unit = ?", v)
	}
	if v := c.Query("business_unit_item"); v != "" {
		query = query.Where("business_unit_item = ?", v)
	}
	if v := c.Query("channel"); v != "" {
		query = query.Where("channel = ?", v)
	}
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("company_tag"); v != "" {
		query = query.Joins("JOIN companies ON companies.id = deals.company_id").
			Where("companies.tags && ARRAY[?]::text[]", v)
	}
	return query
}

func periodStart(period string) (time.Time, bool) {
	now := time.Now()
	switch period {
	case "month":
		return now.AddDate(0, -1, 0), true
	case "quarter":
		return now.AddDate(0, -3, 0), true
	case "year", "last12":
		return now.AddDate(-1, 0, 0), true
	case "last6":
		return now.AddDate(0, -6, 0), true
	default:
		return time.Time{}, false
	}
}

// winRate is the won/(won+lost) formula shared by Summary, industryBreakdown,
// and teamPerformance — kept in one place so it stays consistent everywhere.
func winRate(won, lost int64) float64 {
	if won+lost == 0 {
		return 0
	}
	return float64(won) / float64(won+lost) * 100
}

type revenueTrendPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

type stageBreakdownItem struct {
	Stage models.DealStage `json:"stage"`
	Value float64          `json:"value"`
	Count int64            `json:"count"`
}

type industryBreakdownItem struct {
	Industry string  `json:"industry"`
	WinRate  float64 `json:"win_rate"`
	WonCount int64   `json:"won_count"`
}

type teamPerformanceItem struct {
	UserID   uint    `json:"user_id"`
	Name     string  `json:"name"`
	WonCount int64   `json:"won_count"`
	WonValue float64 `json:"won_value"`
	WinRate  float64 `json:"win_rate"`
}

// Summary — GET /dashboard/summary. api-system-spec.md §9.
func (h *DashboardHandler) Summary(c *fiber.Ctx) error {
	base := h.baseFilter(c)

	var openPipelineValue, wonValue, avgDealSize float64
	base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusOpen).
		Select("COALESCE(SUM(value), 0)").Scan(&openPipelineValue)
	base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusWon).
		Select("COALESCE(SUM(value), 0)").Scan(&wonValue)
	base.Session(&gorm.Session{}).Select("COALESCE(AVG(value), 0)").Scan(&avgDealSize)

	var openDealsCount int64
	base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusOpen).Count(&openDealsCount)

	// forecastedRevenue — sum of (open Deal value × probability/100). Probability
	// defaults per-stage at write time (see StageDefaultProbability) so every open
	// Deal has one, but COALESCE guards any pre-existing row a migration missed.
	var forecastedRevenue float64
	base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusOpen).
		Select("COALESCE(SUM(value * COALESCE(probability, 0) / 100.0), 0)").Scan(&forecastedRevenue)

	var wonCount, lostCount int64
	base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusWon).Count(&wonCount)
	base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusLost).Count(&lostCount)

	pipelineCoverageRatio := 0.0
	if quarterlySalesTarget > 0 {
		pipelineCoverageRatio = openPipelineValue / (quarterlySalesTarget / 4)
	}

	// avg_sales_cycle_days: no stage-transition timestamps are tracked yet, so
	// this is left at 0 until that data exists.
	avgSalesCycleDays := 0

	revenueTrend := h.revenueTrend(c)
	stageBreakdown := h.stageBreakdown(c)
	industryBreakdown := h.industryBreakdown(c)
	teamPerformance := h.teamPerformance(c)

	return utils.OK(c, fiber.Map{
		"open_pipeline_value":     openPipelineValue,
		"won_value":               wonValue,
		"win_rate":                winRate(wonCount, lostCount),
		"open_deals_count":        openDealsCount,
		"forecasted_revenue":      forecastedRevenue,
		"avg_deal_size":           avgDealSize,
		"avg_sales_cycle_days":    avgSalesCycleDays,
		"pipeline_coverage_ratio": pipelineCoverageRatio,
		"quarterly_sales_target":  float64(quarterlySalesTarget),
		"revenue_trend":           revenueTrend,
		"stage_breakdown":         stageBreakdown,
		"industry_breakdown":      industryBreakdown,
		"team_performance":        teamPerformance,
		// upsell_opportunities needs Tag-tier + stale-contact logic not yet
		// specified precisely enough to implement — ship empty rather than guess wrong.
		"upsell_opportunities": []interface{}{},
	})
}

func (h *DashboardHandler) revenueTrend(c *fiber.Ctx) []revenueTrendPoint {
	points := make([]revenueTrendPoint, 0, 6)
	now := time.Now()
	for i := 5; i >= 0; i-- {
		month := now.AddDate(0, -i, 0)
		start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
		end := start.AddDate(0, 1, 0)

		var value float64
		h.DB.Model(&models.Deal{}).
			Where("status = ? AND created_at >= ? AND created_at < ?", models.DealStatusWon, start, end).
			Select("COALESCE(SUM(value), 0)").Scan(&value)

		points = append(points, revenueTrendPoint{Label: start.Format("Jan"), Value: value})
	}
	return points
}

func (h *DashboardHandler) stageBreakdown(c *fiber.Ctx) []stageBreakdownItem {
	var rows []stageBreakdownItem
	h.baseFilter(c).Session(&gorm.Session{}).
		Select("stage, COALESCE(SUM(value), 0) as value, count(*) as count").
		Group("stage").Scan(&rows)
	return rows
}

func (h *DashboardHandler) industryBreakdown(c *fiber.Ctx) []industryBreakdownItem {
	var rows []struct {
		Industry  string
		WonCount  int64
		LostCount int64
	}
	// baseFilter already joins companies when ?company_tag= is set — don't join twice.
	query := h.baseFilter(c).Session(&gorm.Session{})
	if c.Query("company_tag") == "" {
		query = query.Joins("JOIN companies ON companies.id = deals.company_id")
	}
	query.Select("companies.industry as industry, count(*) FILTER (WHERE deals.status = 'won') as won_count, count(*) FILTER (WHERE deals.status = 'lost') as lost_count").
		Group("companies.industry").Scan(&rows)

	result := make([]industryBreakdownItem, 0, len(rows))
	for _, r := range rows {
		result = append(result, industryBreakdownItem{
			Industry: r.Industry, WinRate: winRate(r.WonCount, r.LostCount), WonCount: r.WonCount,
		})
	}
	return result
}

func (h *DashboardHandler) teamPerformance(c *fiber.Ctx) []teamPerformanceItem {
	var rows []struct {
		UserID    uint
		WonCount  int64
		WonValue  float64
		LostCount int64
	}
	h.baseFilter(c).Session(&gorm.Session{}).
		Where("assigned_to IS NOT NULL").
		Select("assigned_to as user_id, count(*) FILTER (WHERE status = 'won') as won_count, COALESCE(SUM(value) FILTER (WHERE status = 'won'), 0) as won_value, count(*) FILTER (WHERE status = 'lost') as lost_count").
		Group("assigned_to").Scan(&rows)

	userIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		userIDs = append(userIDs, r.UserID)
	}
	var users []models.User
	if len(userIDs) > 0 {
		h.DB.Where("id IN ?", userIDs).Find(&users)
	}
	names := make(map[uint]string, len(users))
	for _, u := range users {
		names[u.ID] = u.FirstName + " " + u.LastName
	}

	result := make([]teamPerformanceItem, 0, len(rows))
	for _, r := range rows {
		result = append(result, teamPerformanceItem{
			UserID: r.UserID, Name: names[r.UserID], WonCount: r.WonCount, WonValue: r.WonValue,
			WinRate: winRate(r.WonCount, r.LostCount),
		})
	}
	return result
}

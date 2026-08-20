package handlers

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

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

// appSettings loads the AppSettings singleton row once so both
// quarterlySalesTarget and annualRevenueGoal below don't each issue their own
// query, falling back to the original hardcoded defaults if the row is
// somehow missing (e.g. seed hasn't run yet).
func (h *DashboardHandler) appSettings() models.AppSettings {
	var settings models.AppSettings
	if err := h.DB.First(&settings, 1).Error; err != nil {
		return models.DefaultAppSettings
	}
	return settings
}

type annualGoalTrendPoint struct {
	Label    string  `json:"label"`
	Actual   float64 `json:"actual"`
	GoalPace float64 `json:"goal_pace"`
}

// annualRevenueTrend buckets cumulative Won Deal value for the current
// calendar year, Jan through the current month, alongside a straight-line
// "goal pace" for the same point (annualGoal × months-elapsed/12) — lets the
// frontend chart whether the company is ahead of or behind pace over the
// year, not just infer it from today's single ratio. A fixed company-wide
// figure, deliberately ignoring Summary's base filter (business_unit/
// channel/assigned_to/company_tag/date range) the same way revenueTrend/
// forecastTrend do, since the annual goal (FR-CRM-091) tracks the whole
// company against one company-wide target, not a filtered slice. The last
// point's Actual also doubles as annual_revenue_actual in Summary's response
// — one grouped query instead of a duplicate SUM.
func (h *DashboardHandler) annualRevenueTrend(annualGoal int64) []annualGoalTrendPoint {
	now := time.Now()
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())

	var rows []struct {
		MonthKey string
		Value    float64
	}
	h.DB.Model(&models.Deal{}).
		Where("status = ? AND created_at >= ?", models.DealStatusWon, yearStart).
		Select("to_char(created_at, 'YYYY-MM') as month_key, COALESCE(SUM(value), 0) as value").
		Group("month_key").Scan(&rows)

	byMonth := make(map[string]float64, len(rows))
	for _, r := range rows {
		byMonth[r.MonthKey] = r.Value
	}

	monthsElapsed := int(now.Month())
	points := make([]annualGoalTrendPoint, 0, monthsElapsed)
	cumulative := 0.0
	for i := 0; i < monthsElapsed; i++ {
		month := yearStart.AddDate(0, i, 0)
		cumulative += byMonth[month.Format("2006-01")]
		points = append(points, annualGoalTrendPoint{
			Label:    month.Format("Jan"),
			Actual:   cumulative,
			GoalPace: float64(annualGoal) * float64(i+1) / 12,
		})
	}
	return points
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

// summaryCacheTTL bounds how stale a cached Summary response can be. The
// dashboard is read-heavy (every sales manager's landing page) and its ~10
// underlying aggregate queries are expensive to repeat on every refresh, but
// deal data doesn't need to be second-fresh here — a short TTL trades a small
// amount of staleness for a large cut in DB load under concurrent viewers.
const summaryCacheTTL = 30 * time.Second

type summaryCacheEntry struct {
	body      fiber.Map
	expiresAt time.Time
}

var (
	summaryCacheMu sync.Mutex
	summaryCache   = map[string]summaryCacheEntry{}
)

// InvalidateDashboardCache drops every cached Summary response. The cache is
// keyed per exact querystring, so a targeted invalidation isn't possible
// without re-deriving every filter combination a caller might have used —
// clearing the whole thing is the simple, correct option and this is already
// a rare event (an Admin changing settings), not a hot path.
//
// Call this whenever something Summary's response depends on changes outside
// of a normal Deal/Company write — currently just SettingsHandler.Update,
// since quarterly_sales_target/annual_revenue_goal both feed directly into
// Summary's response (pipeline_coverage_ratio, annual_revenue_goal/
// annual_revenue_progress_ratio) but a settings PATCH doesn't touch the deals
// table at all, so nothing else would ever invalidate this cache for them —
// without this, an Admin changing the annual goal could see the old value
// reflected back on their own dashboard for up to summaryCacheTTL.
func InvalidateDashboardCache() {
	summaryCacheMu.Lock()
	defer summaryCacheMu.Unlock()
	summaryCache = map[string]summaryCacheEntry{}
}

// ResetDashboardCacheForTests is InvalidateDashboardCache under a
// test-specific name — the integration suite truncates and reseeds the
// shared test DB between tests but this cache is process-lifetime, keyed
// only by querystring, so two tests both hitting GET /dashboard/summary with
// no params would otherwise share a cache entry and one could serve the
// other's stale numbers within the TTL.
func ResetDashboardCacheForTests() {
	InvalidateDashboardCache()
}

// Summary — GET /dashboard/summary. api-system-spec.md §9.
func (h *DashboardHandler) Summary(c *fiber.Ctx) error {
	cacheKey := string(c.Request().URI().QueryString())
	summaryCacheMu.Lock()
	if entry, ok := summaryCache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		summaryCacheMu.Unlock()
		return utils.OK(c, entry.body)
	}
	summaryCacheMu.Unlock()

	base := h.baseFilter(c)
	// Read every query param the concurrent goroutines below need up front,
	// on this goroutine, before any of them start. c.Query(...) reads/lazily
	// parses fasthttp's shared, unsynchronized query-args cache on first
	// access per request — calling it from multiple goroutines at once (as an
	// earlier version of this handler did, via each breakdown method calling
	// h.baseFilter(c) for itself) is a data race. base already resolves every
	// filter into gorm clauses synchronously right here; companyTagSet is the
	// one extra bit industryBreakdown needs to avoid double-joining companies.
	companyTagSet := c.Query("company_tag") != ""

	// Loaded synchronously up front (one cheap query) rather than after
	// wg.Wait() below, since annualRevenueTrend needs settings.AnnualRevenueGoal
	// and runs inside that same concurrent block.
	settings := h.appSettings()

	// These 5 base aggregates plus the 6 breakdown/trend helpers below are all
	// independent read-only queries against the same filter — run them
	// concurrently instead of serially so wall-clock time is roughly the
	// slowest single query, not the sum of all ~11. None of them touch `c`
	// (or anything else fiber-request-shaped) from here on, only `base` and
	// plain values already captured above — see the comment on that.
	var openPipelineValue, wonValue, avgDealSize, forecastedRevenue float64
	var openDealsCount, wonCount, lostCount int64
	var revenueTrend, forecastTrend []revenueTrendPoint
	var stageBreakdown []stageBreakdownItem
	var industryBreakdown []industryBreakdownItem
	var teamPerformance []teamPerformanceItem
	var annualRevenueTrend []annualGoalTrendPoint

	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}

	run(func() {
		base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusOpen).
			Select("COALESCE(SUM(value), 0)").Scan(&openPipelineValue)
	})
	run(func() {
		base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusWon).
			Select("COALESCE(SUM(value), 0)").Scan(&wonValue)
	})
	run(func() {
		base.Session(&gorm.Session{}).Select("COALESCE(AVG(value), 0)").Scan(&avgDealSize)
	})
	run(func() {
		base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusOpen).Count(&openDealsCount)
	})
	// forecastedRevenue — sum of (open Deal value × probability/100). Probability
	// defaults per-stage at write time (see StageDefaultProbability) so every open
	// Deal has one, but COALESCE guards any pre-existing row a migration missed.
	run(func() {
		base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusOpen).
			Select("COALESCE(SUM(value * COALESCE(probability, 0) / 100.0), 0)").Scan(&forecastedRevenue)
	})
	run(func() {
		base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusWon).Count(&wonCount)
	})
	run(func() {
		base.Session(&gorm.Session{}).Where("status = ?", models.DealStatusLost).Count(&lostCount)
	})
	run(func() { revenueTrend = h.revenueTrend() })
	run(func() { forecastTrend = h.forecastTrend() })
	run(func() { stageBreakdown = h.stageBreakdown(base) })
	run(func() { industryBreakdown = h.industryBreakdown(base, companyTagSet) })
	run(func() { teamPerformance = h.teamPerformance(base) })
	run(func() { annualRevenueTrend = h.annualRevenueTrend(settings.AnnualRevenueGoal) })
	wg.Wait()

	quarterlySalesTarget := settings.QuarterlySalesTarget
	pipelineCoverageRatio := 0.0
	if quarterlySalesTarget > 0 {
		pipelineCoverageRatio = openPipelineValue / (float64(quarterlySalesTarget) / 4)
	}

	// annualRevenueActual is the trend's last cumulative point (Jan through
	// the current month) rather than a duplicate SUM query — annualRevenueTrend
	// always has at least one point since now.Month() is never 0.
	annualRevenueGoal := settings.AnnualRevenueGoal
	annualRevenueActual := annualRevenueTrend[len(annualRevenueTrend)-1].Actual
	annualRevenueProgressRatio := 0.0
	if annualRevenueGoal > 0 {
		annualRevenueProgressRatio = annualRevenueActual / float64(annualRevenueGoal)
	}

	// avg_sales_cycle_days: no stage-transition timestamps are tracked yet, so
	// this is left at 0 until that data exists.
	avgSalesCycleDays := 0

	body := fiber.Map{
		"open_pipeline_value":           openPipelineValue,
		"won_value":                     wonValue,
		"win_rate":                      winRate(wonCount, lostCount),
		"open_deals_count":              openDealsCount,
		"forecasted_revenue":            forecastedRevenue,
		"avg_deal_size":                 avgDealSize,
		"avg_sales_cycle_days":          avgSalesCycleDays,
		"pipeline_coverage_ratio":       pipelineCoverageRatio,
		"quarterly_sales_target":        float64(quarterlySalesTarget),
		"annual_revenue_goal":           float64(annualRevenueGoal),
		"annual_revenue_actual":         annualRevenueActual,
		"annual_revenue_progress_ratio": annualRevenueProgressRatio,
		"annual_revenue_trend":          annualRevenueTrend,
		"revenue_trend":                 revenueTrend,
		"forecast_trend":                forecastTrend,
		"stage_breakdown":               stageBreakdown,
		"industry_breakdown":            industryBreakdown,
		"team_performance":              teamPerformance,
		// upsell_opportunities needs Tag-tier + stale-contact logic not yet
		// specified precisely enough to implement — ship empty rather than guess wrong.
		"upsell_opportunities": []interface{}{},
	}

	summaryCacheMu.Lock()
	summaryCache[cacheKey] = summaryCacheEntry{body: body, expiresAt: time.Now().Add(summaryCacheTTL)}
	summaryCacheMu.Unlock()

	return utils.OK(c, body)
}

// revenueTrend buckets the last 6 months (this month + 5 back) of Won revenue
// by created_at month, in a single grouped query rather than one query per
// month — the original shape issued 6 round-trips here on every dashboard
// load. Deliberately ignores Summary's base filter (business_unit/channel/
// assigned_to/company_tag/date range) — it's a fixed trailing-6-month view
// independent of those, same as forecastTrend below.
func (h *DashboardHandler) revenueTrend() []revenueTrendPoint {
	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	rangeStart := thisMonthStart.AddDate(0, -5, 0)
	rangeEnd := thisMonthStart.AddDate(0, 1, 0)

	var rows []struct {
		MonthKey string
		Value    float64
	}
	h.DB.Model(&models.Deal{}).
		Where("status = ? AND created_at >= ? AND created_at < ?", models.DealStatusWon, rangeStart, rangeEnd).
		Select("to_char(created_at, 'YYYY-MM') as month_key, COALESCE(SUM(value), 0) as value").
		Group("month_key").Scan(&rows)

	byMonth := make(map[string]float64, len(rows))
	for _, r := range rows {
		byMonth[r.MonthKey] = r.Value
	}

	points := make([]revenueTrendPoint, 0, 6)
	for i := 5; i >= 0; i-- {
		month := now.AddDate(0, -i, 0)
		points = append(points, revenueTrendPoint{Label: month.Format("Jan"), Value: byMonth[month.Format("2006-01")]})
	}
	return points
}

// forecastTrend is the forward-looking counterpart to revenueTrend: instead of
// bucketing past Won revenue by created_at month, it buckets open deals'
// probability-weighted value by ExpectedCloseDate month for the next 6 months
// (this month + 5 forward), mirroring revenueTrend's exact date-window shape,
// collapsed the same way into one grouped query instead of one per month.
//
// ExpectedCloseDate is a nullable *string (not required at Create), so deals
// without one cannot be placed in a month bucket here and are excluded from
// every point below. They are NOT excluded from the headline forecasted_revenue
// stat card above, which sums all open deals regardless of date — so this
// trend's points may sum to less than that headline total. The frontend must
// not present this breakdown as the complete forecast.
//
// Groups by the date string's first 7 characters ("YYYY-MM") rather than
// casting expected_close_date to a real date type — it's stored as free-form
// text (see the type note below) and a LEFT()-based string group-by tolerates
// both the plain "2006-01-02" and full ISO-datetime forms without risking a
// cast failure aborting the whole query over one malformed row.
func (h *DashboardHandler) forecastTrend() []revenueTrendPoint {
	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	rangeStart := thisMonthStart
	rangeEnd := thisMonthStart.AddDate(0, 6, 0)

	// expected_close_date is stored as text (no explicit gorm type on the
	// nullable *string field), holding either a plain "2006-01-02" date or a
	// full ISO datetime (the frontend submits Date objects, which
	// JSON-serialize to e.g. "2026-08-17T00:00:00.000Z"). Comparing against
	// plain YYYY-MM-DD bounds still buckets correctly either way: it's a
	// lexicographic string comparison, and since both forms share the same
	// zero-padded date prefix, "<bound>" sorts before any same-day timestamp
	// string and after the prior day's, so month windows land on the right
	// boundary regardless of which format is stored.
	var rows []struct {
		MonthKey string
		Value    float64
	}
	h.DB.Model(&models.Deal{}).
		Where("status = ? AND expected_close_date >= ? AND expected_close_date < ?",
			models.DealStatusOpen, rangeStart.Format("2006-01-02"), rangeEnd.Format("2006-01-02")).
		Select("LEFT(expected_close_date, 7) as month_key, COALESCE(SUM(value * COALESCE(probability, 0) / 100.0), 0) as value").
		Group("month_key").Scan(&rows)

	byMonth := make(map[string]float64, len(rows))
	for _, r := range rows {
		byMonth[r.MonthKey] = r.Value
	}

	points := make([]revenueTrendPoint, 0, 6)
	for i := 0; i <= 5; i++ {
		month := now.AddDate(0, i, 0)
		points = append(points, revenueTrendPoint{Label: month.Format("Jan"), Value: byMonth[month.Format("2006-01")]})
	}
	return points
}

// stageBreakdown, industryBreakdown, and teamPerformance all take the already
// -built base filter query (from Summary's single synchronous h.baseFilter(c)
// call) rather than *fiber.Ctx — Summary runs these concurrently via
// goroutines, and re-deriving the filter from c in each one used to mean
// several goroutines calling c.Query(...) at once, which is a data race on
// fasthttp's shared, lazily-parsed query-args cache (it mutates on first
// access per request with no locking). Passing the pre-built *gorm.DB in
// avoids touching c from any of these at all.
func (h *DashboardHandler) stageBreakdown(base *gorm.DB) []stageBreakdownItem {
	var rows []stageBreakdownItem
	base.Session(&gorm.Session{}).
		Select("stage, COALESCE(SUM(value), 0) as value, count(*) as count").
		Group("stage").Scan(&rows)
	return rows
}

// companyTagSet mirrors whether Summary's base filter already joined
// companies (only when ?company_tag= was supplied) — avoids joining it twice.
func (h *DashboardHandler) industryBreakdown(base *gorm.DB, companyTagSet bool) []industryBreakdownItem {
	var rows []struct {
		Industry  string
		WonCount  int64
		LostCount int64
	}
	query := base.Session(&gorm.Session{})
	if !companyTagSet {
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

func (h *DashboardHandler) teamPerformance(base *gorm.DB) []teamPerformanceItem {
	var rows []struct {
		UserID    uint
		WonCount  int64
		WonValue  float64
		LostCount int64
	}
	base.Session(&gorm.Session{}).
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

package handlers

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type DashboardHandler struct {
	DB *gorm.DB
}

func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db}
}

// applyDateWindow applies the shared date_from/date_to-or-period resolution
// (explicit bounds win outright; period is only a fallback when both are
// omitted) against the given column. Shared by baseFilter (deals.created_at)
// and teamPerformance's activity-count query (activities.created_at) so the
// two aggregates always agree on what date window "this dashboard view"
// means, rather than each re-deriving it. window is the already-parsed
// date_from/date_to (dateRangeQuery: inclusive server-local days).
func applyDateWindow(query *gorm.DB, column string, window utils.DateRange, period string) *gorm.DB {
	if window.IsZero() {
		if from, ok := periodStart(period); ok {
			query = query.Where(column+" >= ?", from)
		}
		return query
	}
	return window.Apply(query, column)
}

// baseFilter applies the shared date_from/date_to (or period), business_unit,
// business_unit_item, channel, assigned_to (Sales Rep), and company_tag query
// params — api-system-spec.md §9, FR-CRM-055.
func (h *DashboardHandler) baseFilter(c *fiber.Ctx, window utils.DateRange) *gorm.DB {
	query := h.DB.Model(&models.Deal{})
	query = applyDateWindow(query, "deals.created_at", window, c.Query("period"))

	if v := c.Query("business_unit"); v != "" {
		query = query.Where("deals.business_unit = ?", v)
	}
	if v := c.Query("business_unit_item"); v != "" {
		query = query.Where("deals.business_unit_item = ?", v)
	}
	if v := c.Query("channel"); v != "" {
		query = query.Where("deals.channel = ?", v)
	}
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("deals.assigned_to = ?", v)
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

// currentQuarterTarget resolves the actual target to use for THIS calendar
// quarter's pipeline_coverage_ratio (FR-CRM-092): a SalesTarget row for the
// current (year, quarter) if an Admin has set one, else the flat
// AppSettings.QuarterlySalesTarget/4 fallback (today's pre-FR-CRM-092
// behavior, unchanged for anyone who's never touched the new feature).
func (h *DashboardHandler) currentQuarterTarget(fallbackAnnual int64) float64 {
	now := time.Now()
	quarter := (int(now.Month())-1)/3 + 1

	var target models.SalesTarget
	if err := h.DB.Where("year = ? AND quarter = ?", now.Year(), quarter).First(&target).Error; err == nil {
		return float64(target.TargetValue)
	}
	return float64(fallbackAnnual) / 4
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
	now := time.Now().In(time.Local)
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.Local)

	monthsElapsed := int(now.Month())
	bounds := monthBounds(yearStart, monthsElapsed)
	byMonth := sumByLocalMonth(h.DB.Model(&models.Deal{}).Where("status = ?", models.DealStatusWon),
		"created_at", "value", bounds)

	points := make([]annualGoalTrendPoint, 0, monthsElapsed)
	cumulative := 0.0
	for i := 0; i < monthsElapsed; i++ {
		cumulative += byMonth[i]
		points = append(points, annualGoalTrendPoint{
			Label:    bounds[i].Format("Jan"),
			Actual:   cumulative,
			GoalPace: float64(annualGoal) * float64(i+1) / 12,
		})
	}
	return points
}

// monthBounds returns the server-local month starts of n consecutive months
// from first (itself a month start), plus the start of the month after:
// n+1 bounds, month i being [bounds[i], bounds[i+1]). Stepping from the 1st
// is what keeps AddDate from rolling over — now.AddDate(0, -1, 0) on 31
// March normalizes 31 February to 3 March, repeating March and skipping
// February in a trend.
func monthBounds(first time.Time, n int) []time.Time {
	bounds := make([]time.Time, n+1)
	for i := range bounds {
		bounds[i] = first.AddDate(0, i, 0)
	}
	return bounds
}

// sumByLocalMonth sums valueExpr over query's rows into len(bounds)-1
// monthly buckets by the timestamp column, in one grouped query. The month
// edges are monthBounds' server-local midnights (TZ, Asia/Bangkok), matched
// with width_bucket, rather than to_char(column, 'YYYY-MM'), which splits
// months at the DB session's midnight (UTC) — putting a Deal won before
// 07:00 Bangkok on the 1st in the previous month. column must be qualified
// if query joins another table.
func sumByLocalMonth(query *gorm.DB, column, valueExpr string, bounds []time.Time) []float64 {
	sums := make([]float64, len(bounds)-1)
	placeholders := make([]string, len(bounds))
	args := make([]interface{}, len(bounds))
	for i, b := range bounds {
		placeholders[i], args[i] = "?", b
	}
	var rows []struct {
		Bucket int
		Value  float64
	}
	query.Where(column+" >= ? AND "+column+" < ?", bounds[0], bounds[len(bounds)-1]).
		Select("width_bucket("+column+", ARRAY["+strings.Join(placeholders, ", ")+"]::timestamptz[]) as bucket, "+
			"COALESCE(SUM("+valueExpr+"), 0) as value", args...).
		Group("bucket").Scan(&rows)
	for _, r := range rows {
		if r.Bucket >= 1 && r.Bucket <= len(sums) {
			sums[r.Bucket-1] = r.Value
		}
	}
	return sums
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

// forecastByCategoryItem is the probability-weighted forecast for open Deals
// falling under one ForecastCategory — Commit/Best Case/Pipeline breaking
// down the single forecastedRevenue figure so it can be audited rather than
// just trusted as one blended number.
type forecastByCategoryItem struct {
	Commit   float64 `json:"commit"`
	BestCase float64 `json:"best_case"`
	Pipeline float64 `json:"pipeline"`
}

type industryBreakdownItem struct {
	Industry string  `json:"industry"`
	WinRate  float64 `json:"win_rate"`
	WonCount int64   `json:"won_count"`
}

type teamPerformanceItem struct {
	UserID        uint    `json:"user_id"`
	Name          string  `json:"name"`
	WonCount      int64   `json:"won_count"`
	WonValue      float64 `json:"won_value"`
	WinRate       float64 `json:"win_rate"`
	ActivityCount int64   `json:"activity_count"`
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

// Summary godoc
// @Summary Dashboard summary
// @Description Aggregate sales metrics (pipeline value, win rate, trends, breakdowns, upsell opportunities). api-system-spec.md §9.
// @Tags dashboard
// @Security BearerAuth
// @Produce json
// @Param date_from query string false "ISO date lower bound (YYYY-MM-DD, from server-local midnight), mutually exclusive with period"
// @Param date_to query string false "ISO date upper bound (YYYY-MM-DD, inclusive of that whole server-local day)"
// @Param period query string false "One of: month, quarter, last6, year/last12"
// @Param business_unit query string false "Filter by business unit"
// @Param business_unit_item query string false "Filter by business unit item"
// @Param channel query string false "Filter by Deal channel"
// @Param assigned_to query string false "Filter by Sales Rep user ID"
// @Param company_tag query string false "Filter by Company tag"
// @Param upsell_min_stale_days query int false "Upsell Opportunities staleness threshold in days (default 60)"
// @Success 200 {object} map[string]interface{}
// @Failure 422 {object} map[string]interface{} "Malformed date_from/date_to, or date_to before date_from"
// @Router /dashboard/summary [get]
func (h *DashboardHandler) Summary(c *fiber.Ctx) error {
	// Parsed (and a malformed/reversed range 422'd) before anything reaches
	// Postgres: an invalid bound would otherwise fail each aggregate at the
	// driver level, and Summary's helpers discard Scan's error, silently
	// zeroing the whole dashboard instead of telling the caller.
	window, err := dateRangeQuery(c)
	if err != nil {
		return reportError(c, err, "")
	}

	cacheKey := string(c.Request().URI().QueryString())
	summaryCacheMu.Lock()
	if entry, ok := summaryCache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		summaryCacheMu.Unlock()
		return utils.OK(c, entry.body)
	}
	summaryCacheMu.Unlock()

	base := h.baseFilter(c, window)
	// Read every query param the concurrent goroutines below need up front,
	// on this goroutine, before any of them start. c.Query(...) reads/lazily
	// parses fasthttp's shared, unsynchronized query-args cache on first
	// access per request — calling it from multiple goroutines at once (as an
	// earlier version of this handler did, via each breakdown method calling
	// h.baseFilter(c) for itself) is a data race. base already resolves every
	// filter into gorm clauses synchronously right here; companyTagSet is the
	// one extra bit industryBreakdown needs to avoid double-joining companies.
	companyTagSet := c.Query("company_tag") != ""
	// Same up-front-synchronous-read rule as companyTagSet above — fetchSalesCycle
	// (called from a goroutine below) only takes plain strings, not `c`, for
	// exactly this reason.
	assignedTo, period := c.Query("assigned_to"), c.Query("period")
	// upsell_min_stale_days — the Upsell Opportunities widget's own staleness
	// filter (FR-CRM-108/109), read up front for the same data-race reason as
	// companyTagSet/assignedTo above. Defaults to 60; invalid/non-positive
	// values fall back to it rather than 400ing, since this is a display
	// filter, not a validated form field.
	upsellMinStaleDays := 60
	if v, err := strconv.Atoi(c.Query("upsell_min_stale_days")); err == nil && v > 0 {
		upsellMinStaleDays = v
	}

	// Loaded synchronously up front (one cheap query) rather than after
	// wg.Wait() below, since annualRevenueTrend needs settings.AnnualRevenueGoal
	// and runs inside that same concurrent block.
	settings := utils.GetAppSettings(h.DB)

	// These 5 base aggregates plus the 7 breakdown/trend/target helpers below
	// are all independent read-only queries — run them concurrently instead
	// of serially so wall-clock time is roughly the slowest single query, not
	// the sum of all ~12. None of them touch `c` (or anything else
	// fiber-request-shaped), only `base`/`settings` and plain values already
	// captured above — see the comment on that.
	var openPipelineValue, wonValue, avgDealSize, forecastedRevenue float64
	var openDealsCount, wonCount, lostCount int64
	var revenueTrend, forecastTrend []revenueTrendPoint
	var stageBreakdown []stageBreakdownItem
	var forecastByCategory forecastByCategoryItem
	var industryBreakdown []industryBreakdownItem
	var teamPerformance []teamPerformanceItem
	var annualRevenueTrend []annualGoalTrendPoint
	var upsellOpportunities []upsellCompany

	var wg sync.WaitGroup
	// degradedMu guards degraded below, appended to from whichever aggregate
	// goroutine's recover() fires — see run's own comment.
	var degradedMu sync.Mutex
	degraded := []string{} // non-nil so it serializes as [] rather than null in the common case
	// run fans each named aggregate out onto its own goroutine — see the
	// comment above for why this is safe to do concurrently. Built on
	// utils.SafeGoNotify (rather than a hand-rolled recover() wrapper) so
	// this shares its one panic-recovery/logging implementation with every
	// other background goroutine in the codebase (apikey.go,
	// open_api_log.go) instead of drifting from it. recover() here is
	// load-bearing, not defensive boilerplate: without it, a panic in any
	// single one of the ~14 closures below (a nil dereference from an
	// unexpected scan shape, a slice index, ...) would be an unrecovered
	// panic in a goroutine, which crashes the entire process — every other
	// in-flight request too, not just this one. name is recorded into the
	// response's own `degraded_aggregates` field (rather than only the
	// server log) if this aggregate's goroutine panics, so a caller/on-call
	// engineer can tell "this number is genuinely zero" apart from
	// "this number silently failed to compute" instead of the two looking
	// identical in the response.
	run := func(name string, f func()) {
		wg.Add(1)
		utils.SafeGoNotify(func() {
			defer wg.Done()
			f()
		}, func(r any) {
			degradedMu.Lock()
			degraded = append(degraded, name)
			degradedMu.Unlock()
		})
	}

	run("open_pipeline_value", func() {
		base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusOpen).
			Select("COALESCE(SUM(deals.value), 0)").Scan(&openPipelineValue)
	})
	run("won_value", func() {
		base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusWon).
			Select("COALESCE(SUM(deals.value), 0)").Scan(&wonValue)
	})
	run("avg_deal_size", func() {
		base.Session(&gorm.Session{}).Select("COALESCE(AVG(deals.value), 0)").Scan(&avgDealSize)
	})
	run("open_deals_count", func() {
		base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusOpen).Count(&openDealsCount)
	})
	// forecastedRevenue — sum of (open Deal value × probability/100). Probability
	// defaults per-stage at write time (see StageDefaultProbability) so every open
	// Deal has one, but COALESCE guards any pre-existing row a migration missed.
	run("forecasted_revenue", func() {
		base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusOpen).
			Select("COALESCE(SUM(deals.value * COALESCE(deals.probability, 0) / 100.0), 0)").Scan(&forecastedRevenue)
	})
	run("win_rate", func() {
		base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusWon).Count(&wonCount)
	})
	run("win_rate", func() {
		base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusLost).Count(&lostCount)
	})
	run("revenue_trend", func() { revenueTrend = h.revenueTrend() })
	run("forecast_trend", func() { forecastTrend = h.forecastTrend() })
	run("stage_breakdown", func() { stageBreakdown = h.stageBreakdown(base) })
	run("forecast_by_category", func() { forecastByCategory = h.forecastByCategory(base) })
	run("industry_breakdown", func() { industryBreakdown = h.industryBreakdown(base, companyTagSet) })
	run("team_performance", func() { teamPerformance = h.teamPerformance(base, window, period) })
	run("annual_revenue_trend", func() { annualRevenueTrend = h.annualRevenueTrend(settings.AnnualRevenueGoal) })
	// upsellOpportunities is Company-centric (not Deal-scoped), so it's
	// deliberately independent of `base`/baseFilter's Deal-side query params —
	// see h.upsellOpportunities's own doc comment.
	run("upsell_opportunities", func() { upsellOpportunities = h.upsellOpportunities(upsellMinStaleDays) })
	var quarterlySalesTarget float64
	run("quarterly_sales_target", func() { quarterlySalesTarget = h.currentQuarterTarget(settings.QuarterlySalesTarget) })
	// FR-CRM-099's report computation, reused here rather than duplicated —
	// only assigned_to/date_from/date_to are honored (business_unit/channel/
	// company_tag are not, same as annual_revenue_actual's precedent of not
	// applying every dashboard filter to every figure). A query error just
	// leaves this at 0 rather than failing the whole dashboard summary.
	var avgSalesCycleDaysRaw float64
	run("avg_sales_cycle_days", func() {
		if result, err := (&ReportHandler{DB: h.DB}).fetchSalesCycle(assignedTo, window); err == nil {
			if v, ok := result["avg_sales_cycle_days"].(float64); ok {
				avgSalesCycleDaysRaw = v
			}
		}
	})
	wg.Wait()

	pipelineCoverageRatio := 0.0
	if quarterlySalesTarget > 0 {
		pipelineCoverageRatio = openPipelineValue / quarterlySalesTarget
	}

	// annualRevenueActual is the trend's last cumulative point (Jan through
	// the current month) rather than a duplicate SUM query — annualRevenueTrend
	// normally always has at least one point since now.Month() is never 0,
	// but that guarantee only holds if h.annualRevenueTrend actually ran:
	// if its own goroutine above panicked and got recovered, annualRevenueTrend
	// is left at its zero value (nil), and indexing the last element of a nil
	// slice would itself panic — in the request's own goroutine this time,
	// which utils.SafeGoNotify's recover() can't reach. Guard it explicitly
	// rather than relying on an invariant that no longer holds once one
	// aggregate has already degraded.
	annualRevenueGoal := settings.AnnualRevenueGoal
	annualRevenueActual := 0.0
	if len(annualRevenueTrend) > 0 {
		annualRevenueActual = annualRevenueTrend[len(annualRevenueTrend)-1].Actual
	}
	annualRevenueProgressRatio := 0.0
	if annualRevenueGoal > 0 {
		annualRevenueProgressRatio = annualRevenueActual / float64(annualRevenueGoal)
	}

	avgSalesCycleDays := int(math.Round(avgSalesCycleDaysRaw))

	body := fiber.Map{
		"open_pipeline_value":           openPipelineValue,
		"won_value":                     wonValue,
		"win_rate":                      winRate(wonCount, lostCount),
		"open_deals_count":              openDealsCount,
		"forecasted_revenue":            forecastedRevenue,
		"forecast_by_category":          forecastByCategory,
		"avg_deal_size":                 avgDealSize,
		"avg_sales_cycle_days":          avgSalesCycleDays,
		"pipeline_coverage_ratio":       pipelineCoverageRatio,
		"quarterly_sales_target":        quarterlySalesTarget,
		"annual_revenue_goal":           float64(annualRevenueGoal),
		"annual_revenue_actual":         annualRevenueActual,
		"annual_revenue_progress_ratio": annualRevenueProgressRatio,
		"annual_revenue_trend":          annualRevenueTrend,
		"revenue_trend":                 revenueTrend,
		"forecast_trend":                forecastTrend,
		"stage_breakdown":               stageBreakdown,
		"industry_breakdown":            industryBreakdown,
		"team_performance":              teamPerformance,
		"upsell_opportunities":          upsellOpportunities,
		// degraded_aggregates names any of the fields above whose own
		// goroutine (see run's own comment) panicked and got recovered
		// rather than actually computing — always present, empty in the
		// normal case, so a caller can tell "this number is really zero"
		// apart from "this number silently failed" instead of the two
		// looking identical. Never cached stale: this dashboard IS cached
		// (summaryCache below), but a degraded response is never written
		// into it — see the skip-caching check just below.
		"degraded_aggregates": degraded,
	}

	// A degraded response (one or more aggregates panicked) is never cached
	// — caching a transient failure would keep serving it for the rest of
	// summaryCacheTTL even after whatever caused the panic (bad data, a
	// flaky query) has cleared up on its own.
	if len(degraded) == 0 {
		summaryCacheMu.Lock()
		summaryCache[cacheKey] = summaryCacheEntry{body: body, expiresAt: time.Now().Add(summaryCacheTTL)}
		summaryCacheMu.Unlock()
	}

	return utils.OK(c, body)
}

// revenueTrend buckets the last 6 months (this month + 5 back) of Won revenue
// by created_at month, in a single grouped query rather than one query per
// month — the original shape issued 6 round-trips here on every dashboard
// load. Deliberately ignores Summary's base filter (business_unit/channel/
// assigned_to/company_tag/date range) — it's a fixed trailing-6-month view
// independent of those, same as forecastTrend below.
func (h *DashboardHandler) revenueTrend() []revenueTrendPoint {
	bounds := monthBounds(thisMonthStart(time.Now()).AddDate(0, -5, 0), 6)
	byMonth := sumByLocalMonth(h.DB.Model(&models.Deal{}).Where("status = ?", models.DealStatusWon),
		"created_at", "value", bounds)

	points := make([]revenueTrendPoint, 0, 6)
	for i, v := range byMonth {
		points = append(points, revenueTrendPoint{Label: bounds[i].Format("Jan"), Value: v})
	}
	return points
}

// thisMonthStart is server-local midnight on the 1st of now's month.
func thisMonthStart(now time.Time) time.Time {
	now = now.In(time.Local)
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
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
// expected_close_date is free-form text (no explicit gorm type on the
// nullable *string field), holding either a plain "2006-01-02" date or a
// full ISO datetime (the frontend submits Date objects, which
// JSON-serialize to e.g. "2026-08-31T17:00:00.000Z" — 1 September in
// Bangkok). Taking its first 7 characters put that in August, so each
// Deal's month is read in Go instead (calendar.ParseLocalDay: a bare
// date as written, a timestamp by its server-local date). SQL only
// narrows by a string range padded a day each side (a timestamp's UTC date
// can be a day before its local one); a malformed row is skipped rather
// than aborting a cast.
func (h *DashboardHandler) forecastTrend() []revenueTrendPoint {
	start := thisMonthStart(time.Now())
	bounds := monthBounds(start, 6)

	var rows []struct {
		ExpectedCloseDate string
		Value             float64
	}
	h.DB.Model(&models.Deal{}).
		Where("status = ? AND expected_close_date >= ? AND expected_close_date < ?",
			models.DealStatusOpen, start.AddDate(0, 0, -1).Format("2006-01-02"), bounds[6].AddDate(0, 0, 1).Format("2006-01-02")).
		Select("expected_close_date, value * COALESCE(probability, 0) / 100.0 as value").
		Scan(&rows)

	points := make([]revenueTrendPoint, 6)
	for i := range points {
		points[i].Label = bounds[i].Format("Jan")
	}
	for _, r := range rows {
		day, ok := calendar.ParseLocalDay(r.ExpectedCloseDate)
		if !ok {
			continue
		}
		i := (day.Year()-start.Year())*12 + int(day.Month()) - int(start.Month())
		if i >= 0 && i < len(points) {
			points[i].Value += r.Value
		}
	}
	return points
}

// stageBreakdown, industryBreakdown, and teamPerformance all take the already
// -built base filter query (from Summary's single synchronous h.baseFilter(c)
// call) rather than *fiber.Ctx — Summary runs these concurrently, and
// c.Query from several goroutines at once is a data race on fasthttp's
// lazily-parsed query-args cache.
func (h *DashboardHandler) stageBreakdown(base *gorm.DB) []stageBreakdownItem {
	var rows []stageBreakdownItem
	base.Session(&gorm.Session{}).
		Select("deals.stage, COALESCE(SUM(deals.value), 0) as value, count(*) as count").
		Group("deals.stage").Scan(&rows)
	return rows
}

// forecastByCategory splits forecastedRevenue's same weighted formula
// (value × probability/100) across the three ForecastCategory buckets for
// open Deals. A Deal with no category (pre-migration rows never backfilled)
// falls under Pipeline, matching StageDefaultForecastCategory's own fallback.
func (h *DashboardHandler) forecastByCategory(base *gorm.DB) forecastByCategoryItem {
	var rows []struct {
		Category string
		Value    float64
	}
	base.Session(&gorm.Session{}).Where("deals.status = ?", models.DealStatusOpen).
		Select("COALESCE(deals.forecast_category, 'Pipeline') as category, " +
			"COALESCE(SUM(deals.value * COALESCE(deals.probability, 0) / 100.0), 0) as value").
		Group("category").Scan(&rows)

	var result forecastByCategoryItem
	for _, r := range rows {
		switch models.ForecastCategory(r.Category) {
		case models.ForecastCategoryCommit:
			result.Commit = r.Value
		case models.ForecastCategoryBestCase:
			result.BestCase = r.Value
		default:
			result.Pipeline += r.Value
		}
	}
	return result
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

func (h *DashboardHandler) teamPerformance(base *gorm.DB, window utils.DateRange, period string) []teamPerformanceItem {
	var rows []struct {
		UserID    uint
		WonCount  int64
		WonValue  float64
		LostCount int64
	}
	base.Session(&gorm.Session{}).
		Where("deals.assigned_to IS NOT NULL").
		Select("deals.assigned_to as user_id, count(*) FILTER (WHERE deals.status = 'won') as won_count, COALESCE(SUM(deals.value) FILTER (WHERE deals.status = 'won'), 0) as won_value, count(*) FILTER (WHERE deals.status = 'lost') as lost_count").
		Group("deals.assigned_to").Scan(&rows)

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

	// Activity count per rep, over the same date window as the deal-count
	// aggregates above (FR-CRM-053) — a separate query since Activity isn't
	// joined to Deal, mirroring baseFilter's own date_from/date_to/period
	// resolution rather than sharing it (base is scoped to deals.*, not a
	// generically reusable filter).
	activityCounts := make(map[uint]int64, len(userIDs))
	if len(userIDs) > 0 {
		activityQuery := applyDateWindow(h.DB.Model(&models.Activity{}).Where("created_by_id IN ?", userIDs),
			"activities.created_at", window, period)
		var activityRows []struct {
			CreatedByID uint
			Count       int64
		}
		activityQuery.Select("created_by_id, count(*) as count").Group("created_by_id").Scan(&activityRows)
		for _, r := range activityRows {
			activityCounts[r.CreatedByID] = r.Count
		}
	}

	result := make([]teamPerformanceItem, 0, len(rows))
	for _, r := range rows {
		result = append(result, teamPerformanceItem{
			UserID: r.UserID, Name: names[r.UserID], WonCount: r.WonCount, WonValue: r.WonValue,
			WinRate: winRate(r.WonCount, r.LostCount), ActivityCount: activityCounts[r.UserID],
		})
	}
	return result
}

// upsellCap bounds the widget's payload to the upsellCap most-stale
// companies, regardless of how many qualify at the requested threshold.
// Was 10-per-tier (3 tiers, so up to 30 companies) back when this returned
// tier groups; kept the same overall ceiling now that it's one flat list.
const upsellCap = 30

type upsellCompany struct {
	ID             uint       `json:"id"`
	Name           string     `json:"name"`
	Industry       string     `json:"industry"`
	LastActivityAt *time.Time `json:"last_activity_at"`
}

// upsellOpportunities — the Dashboard's "Upsell Opportunities" widget
// (FR-CRM-108): active Companies whose last_activity_at (company_activity.go's
// withLastActivityAt — company-scoped Activities only, NOT rolled up from
// Deals/Contacts) is NULL (never contacted) or at least minStaleDays old,
// most-stale first, capped at upsellCap. minStaleDays is the widget's own
// filter dropdown (?upsell_min_stale_days).
//
// Deliberately independent of Summary's baseFilter (business_unit/channel/
// assigned_to/company_tag/date range) — this is Company-centric, not
// Deal-scoped, same reasoning as annualRevenueTrend/revenueTrend/forecastTrend
// ignoring those filters.
func (h *DashboardHandler) upsellOpportunities(minStaleDays int) []upsellCompany {
	cutoff := time.Now().AddDate(0, 0, -minStaleDays)

	var candidates []upsellCompany
	withLastActivityAt(h.DB.Model(&models.Company{})).
		Where("companies.status = ?", models.StatusActive).
		Where("last_company_activity.last_activity_at IS NULL OR last_company_activity.last_activity_at < ?", cutoff).
		Select("companies.id, companies.name, companies.industry, last_company_activity.last_activity_at as last_activity_at").
		Order("last_company_activity.last_activity_at ASC NULLS FIRST").
		Limit(upsellCap).
		Scan(&candidates)

	return candidates
}

// forecastAccuracyQuarter is one (Year, Quarter) row in the forecast-accuracy
// history — the last snapshot taken during that quarter (its Weighted
// Forecast/category split being the "final word" forecast before the quarter
// closed) paired with that snapshot's own ActualWonToDate. For the current,
// still-open quarter this is simply its most recent snapshot so far.
type forecastAccuracyQuarter struct {
	Year             int     `json:"year"`
	Quarter          int     `json:"quarter"`
	SnapshotDate     string  `json:"snapshot_date"`
	CommitValue      float64 `json:"commit_value"`
	BestCaseValue    float64 `json:"best_case_value"`
	PipelineValue    float64 `json:"pipeline_value"`
	WeightedForecast float64 `json:"weighted_forecast"`
	SalesTarget      float64 `json:"sales_target"`
	ActualWonToDate  float64 `json:"actual_won_to_date"`
	// AccuracyRatio is ActualWonToDate / WeightedForecast, 0 when
	// WeightedForecast is 0 (nothing to divide by — e.g. a very early
	// quarter with no snapshots yet).
	AccuracyRatio float64 `json:"accuracy_ratio"`
}

// ForecastAccuracy godoc
// @Summary Forecast accuracy history (Admin/Sales Manager/Sales Rep)
// @Description Per-quarter forecast-vs-actual history built from the daily ForecastSnapshot job (internal/notifier/forecast_snapshots.go) — the last snapshot of each quarter plus the running snapshot for the current quarter, each with an accuracy_ratio (actual ÷ forecast).
// @Tags dashboard
// @Security BearerAuth
// @Produce json
// @Param quarters query int false "How many most-recent quarters to return (default 8)"
// @Success 200 {array} forecastAccuracyQuarter
// @Router /dashboard/forecast-accuracy [get]
func (h *DashboardHandler) ForecastAccuracy(c *fiber.Ctx) error {
	limitQuarters := 8
	if v, err := strconv.Atoi(c.Query("quarters")); err == nil && v > 0 {
		limitQuarters = v
	}

	// The last snapshot per (year, quarter) — a plain GROUP BY MAX(snapshot_date)
	// then a second lookup, rather than a window function, to keep this
	// portable/readable the same way the rest of this file avoids DB-specific SQL.
	var latestDates []struct {
		Year         int
		Quarter      int
		SnapshotDate time.Time
	}
	if err := h.DB.Model(&models.ForecastSnapshot{}).
		Select("year, quarter, MAX(snapshot_date) as snapshot_date").
		Group("year, quarter").
		Order("year DESC, quarter DESC").
		Limit(limitQuarters).
		Scan(&latestDates).Error; err != nil {
		return utils.Internal(c, "Failed to load forecast accuracy")
	}

	result := make([]forecastAccuracyQuarter, 0, len(latestDates))
	for _, ld := range latestDates {
		var snap models.ForecastSnapshot
		if err := h.DB.Where("year = ? AND quarter = ? AND snapshot_date = ?", ld.Year, ld.Quarter, ld.SnapshotDate).
			First(&snap).Error; err != nil {
			continue
		}
		ratio := 0.0
		if snap.WeightedForecast > 0 {
			ratio = snap.ActualWonToDate / snap.WeightedForecast
		}
		result = append(result, forecastAccuracyQuarter{
			Year: snap.Year, Quarter: snap.Quarter, SnapshotDate: snap.SnapshotDate.Format("2006-01-02"),
			CommitValue: snap.CommitValue, BestCaseValue: snap.BestCaseValue, PipelineValue: snap.PipelineValue,
			WeightedForecast: snap.WeightedForecast, SalesTarget: snap.SalesTarget,
			ActualWonToDate: snap.ActualWonToDate, AccuracyRatio: ratio,
		})
	}
	// Oldest-first for a left-to-right chart, opposite of the DESC query above.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return utils.OK(c, result)
}

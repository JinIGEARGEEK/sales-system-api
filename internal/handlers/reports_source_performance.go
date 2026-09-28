package handlers

import (
	"encoding/csv"
	"sort"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// sourcePerformanceRow — one lead source, followed from Lead all the way to
// Won revenue (FR-CRM-005: the lead-source conversion report stops at
// Qualified).
type sourcePerformanceRow struct {
	Source string `json:"source"`
	// Leads created in the window with this source; Qualified = those that
	// reached Qualified (status Qualified, or already converted to a Deal —
	// conversion leaves status at Qualified, but checking converted_deal_id
	// too keeps a later status edit from hiding a real conversion).
	Leads     int64 `json:"leads"`
	Qualified int64 `json:"qualified"`
	// DealsWon/WonValue: Won Deals that came from those Leads, so far
	// (Σ deals.value, same revenue figure top-referrers uses).
	DealsWon int64   `json:"deals_won"`
	WonValue float64 `json:"won_value"`
	// WinRate = DealsWon / Leads × 100 — lead-to-won, the end-to-end yield
	// of the source. 0 when Leads is 0.
	WinRate float64 `json:"win_rate"`
	// DirectDealsWon/DirectWonValue: Won Deals created in the window with no
	// originating Lead (entered straight into the pipeline), attributed by
	// their own deals.channel. Kept separate so leads → qualified → won stays
	// one cohort and WinRate can't exceed 100%.
	DirectDealsWon int64   `json:"direct_deals_won"`
	DirectWonValue float64 `json:"direct_won_value"`
}

// sourcePerformanceWindow is dateRangeQuery plus the from/to aliases this
// report accepts: date_from/date_to (or from/to), both YYYY-MM-DD and
// inclusive server-local days (utils.ParseDateRange). Returns ok=false after
// writing a 422 for a malformed or reversed range.
func sourcePerformanceWindow(c *fiber.Ctx) (window utils.DateRange, ok bool) {
	pick := func(names ...string) (string, string) {
		for _, name := range names {
			if v := c.Query(name); v != "" {
				return name, v
			}
		}
		return names[0], ""
	}
	fromName, fromValue := pick("date_from", "from")
	toName, toValue := pick("date_to", "to")
	window, msg, fields := utils.ParseDateRange(fromName, fromValue, toName, toValue)
	if fields != nil {
		_ = utils.ValidationError(c, msg, fields)
		return utils.DateRange{}, false
	}
	return window, true
}

// fetchSourcePerformance — shared by SourcePerformance and its CSV export.
//
// Deal → source link: a Lead-originated Deal is attributed to its Lead's
// source, joined through either leads.converted_deal_id or deals.lead_id
// (both are written by the same conversion, leads.go; either alone would
// miss rows if the other were ever cleared). That's more reliable than
// deals.channel, which the rep can edit afterwards and which defaults to
// whatever the convert form sent. Deals with no Lead at all fall back to
// deals.channel and are reported in the direct_* columns.
func (h *ReportHandler) fetchSourcePerformance(c *fiber.Ctx, window utils.DateRange) ([]sourcePerformanceRow, error) {
	assignedTo := c.Query("assigned_to")

	cohort := h.DB.Table("leads").
		Select(`leads.source as source,
			COUNT(DISTINCT leads.id) as leads,
			COUNT(DISTINCT leads.id) FILTER (WHERE leads.status = ? OR leads.converted_deal_id IS NOT NULL) as qualified,
			COUNT(DISTINCT deals.id) FILTER (WHERE deals.status = ?) as deals_won,
			COALESCE(SUM(deals.value) FILTER (WHERE deals.status = ?), 0) as won_value`,
			models.LeadStatusQualified, models.DealStatusWon, models.DealStatusWon).
		Joins("LEFT JOIN deals ON (deals.id = leads.converted_deal_id OR deals.lead_id = leads.id) AND deals.deleted_at IS NULL").
		Where("leads.deleted_at IS NULL").
		Group("leads.source")
	if assignedTo != "" {
		cohort = cohort.Where("leads.assigned_to = ?", assignedTo)
	}
	cohort = window.Apply(cohort, "leads.created_at")
	var cohortRows []sourcePerformanceRow
	if err := cohort.Scan(&cohortRows).Error; err != nil {
		return nil, err
	}

	direct := h.DB.Table("deals").
		Select("deals.channel as source, COUNT(*) as direct_deals_won, COALESCE(SUM(deals.value), 0) as direct_won_value").
		Where("deals.deleted_at IS NULL AND deals.status = ? AND deals.lead_id IS NULL", models.DealStatusWon).
		Where("NOT EXISTS (SELECT 1 FROM leads WHERE leads.converted_deal_id = deals.id)").
		Group("deals.channel")
	if assignedTo != "" {
		direct = direct.Where("deals.assigned_to = ?", assignedTo)
	}
	direct = window.Apply(direct, "deals.created_at")
	var directRows []sourcePerformanceRow
	if err := direct.Scan(&directRows).Error; err != nil {
		return nil, err
	}

	return mergeSourcePerformance(cohortRows, directRows), nil
}

// mergeSourcePerformance combines the Lead-cohort rows and the direct-Deal
// rows by source, computes WinRate, and sorts by Leads, then total won
// value, then source name. Pure, for unit testing.
func mergeSourcePerformance(cohort, direct []sourcePerformanceRow) []sourcePerformanceRow {
	bySource := map[string]*sourcePerformanceRow{}
	order := []string{}
	get := func(source string) *sourcePerformanceRow {
		if r, ok := bySource[source]; ok {
			return r
		}
		r := &sourcePerformanceRow{Source: source}
		bySource[source] = r
		order = append(order, source)
		return r
	}
	for _, c := range cohort {
		r := get(c.Source)
		r.Leads, r.Qualified, r.DealsWon, r.WonValue = r.Leads+c.Leads, r.Qualified+c.Qualified, r.DealsWon+c.DealsWon, r.WonValue+c.WonValue
	}
	for _, d := range direct {
		r := get(d.Source)
		r.DirectDealsWon += d.DirectDealsWon
		r.DirectWonValue += d.DirectWonValue
	}

	rows := make([]sourcePerformanceRow, 0, len(order))
	for _, s := range order {
		r := *bySource[s]
		r.WinRate = conversionRate(r.Leads, r.DealsWon)
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Leads != rows[j].Leads {
			return rows[i].Leads > rows[j].Leads
		}
		vi, vj := rows[i].WonValue+rows[i].DirectWonValue, rows[j].WonValue+rows[j].DirectWonValue
		if vi != vj {
			return vi > vj
		}
		return rows[i].Source < rows[j].Source
	})
	return rows
}

// SourcePerformance godoc
// @Summary Source performance report (Admin/Sales Manager only)
// @Description Per lead source: leads created in the window, how many reached Qualified, how many Won Deals they produced so far and their value, and win_rate = deals_won / leads × 100. Lead-originated Deals are attributed via the Lead (converted_deal_id / deals.lead_id), not deals.channel; Won Deals with no Lead, created in the window, are reported per deals.channel in direct_deals_won/direct_won_value. Dates are YYYY-MM-DD, both inclusive (from/to accepted as aliases); malformed or reversed → 422. FR-CRM-005. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce json
// @Param date_from query string false "Inclusive lower bound (YYYY-MM-DD) on Lead created_at (direct Deals: Deal created_at)"
// @Param date_to query string false "Inclusive upper bound (YYYY-MM-DD)"
// @Param assigned_to query string false "Filter by assigned user ID (Lead's, or the direct Deal's)"
// @Success 200 {array} handlers.sourcePerformanceRow
// @Failure 422 {object} map[string]interface{} "Malformed or reversed date range"
// @Failure 500 {object} map[string]interface{} "Failed to compute source performance"
// @Router /reports/source-performance [get]
func (h *ReportHandler) SourcePerformance(c *fiber.Ctx) error {
	window, ok := sourcePerformanceWindow(c)
	if !ok {
		return nil
	}
	rows, err := h.fetchSourcePerformance(c, window)
	if err != nil {
		return utils.Internal(c, "Failed to compute source performance")
	}
	return utils.OK(c, rows)
}

// SourcePerformanceExport godoc
// @Summary Export source performance report as CSV (Admin/Sales Manager only)
// @Description CSV download of GET /reports/source-performance, same params. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param date_from query string false "Inclusive lower bound (YYYY-MM-DD)"
// @Param date_to query string false "Inclusive upper bound (YYYY-MM-DD)"
// @Param assigned_to query string false "Filter by assigned user ID"
// @Success 200 {file} file "CSV export"
// @Failure 422 {object} map[string]interface{} "Malformed or reversed date range"
// @Failure 500 {object} map[string]interface{} "Failed to export source performance"
// @Router /reports/source-performance/export [get]
func (h *ReportHandler) SourcePerformanceExport(c *fiber.Ctx) error {
	window, ok := sourcePerformanceWindow(c)
	if !ok {
		return nil
	}
	rows, err := h.fetchSourcePerformance(c, window)
	if err != nil {
		return utils.Internal(c, "Failed to export source performance")
	}
	header := []string{"Source", "Leads", "Qualified", "Deals Won", "Won Value", "Win Rate (%)", "Direct Deals Won", "Direct Won Value"}
	return streamCSV(c, "source-performance.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := writeCSVRow(w, []string{
				r.Source, strconv.FormatInt(r.Leads, 10), strconv.FormatInt(r.Qualified, 10),
				strconv.FormatInt(r.DealsWon, 10), strconv.FormatFloat(r.WonValue, 'f', 2, 64),
				strconv.FormatFloat(r.WinRate, 'f', 1, 64),
				strconv.FormatInt(r.DirectDealsWon, 10), strconv.FormatFloat(r.DirectWonValue, 'f', 2, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

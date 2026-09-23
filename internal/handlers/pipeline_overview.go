package handlers

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// pipeline_overview.go — GET /pipeline/overview, the one-request payload behind
// the Overview Pipeline page (FR-CRM-123): Prospect, Lead and Deal lanes on a
// single board, built for a weekly C-level review rather than day-to-day card
// moving (the per-entity Kanban boards stay the place to drag cards).
//
// Two kinds of lane:
//   - open lanes show every record currently in them, whatever the period,
//     since "what's in the pipeline right now" is always current;
//   - terminal lanes (Prospect Converted/Disqualified, Lead Disqualified, Deal
//     Won/Lost) show only records that entered them inside the selected
//     period, or they'd grow forever and bury the review.
//
// "Entered" and "days in stage" both come from stage_entered_at
// (models/stage_entered.go).

type PipelineOverviewHandler struct {
	DB *gorm.DB
}

func NewPipelineOverviewHandler(db *gorm.DB) *PipelineOverviewHandler {
	return &PipelineOverviewHandler{DB: db}
}

const (
	overviewDefaultCardLimit = 30
	overviewMaxCardLimit     = 100
)

// Lane kinds. "open" lanes are always current; the rest are terminal.
const (
	laneOpen      = "open"
	laneWon       = "won"
	laneLost      = "lost"
	laneConverted = "converted"
)

type overviewCard struct {
	ID             uint       `json:"id"`
	Name           string     `json:"name"`
	CompanyID      *uint      `json:"company_id"`
	CompanyName    string     `json:"company_name"`
	AssignedTo     *uint      `json:"assigned_to"`
	Source         string     `json:"source"`
	Value          float64    `json:"value"`
	Probability    *int       `json:"probability"`
	LostReason     *string    `json:"lost_reason"`
	FromProspect   bool       `json:"from_prospect"`
	StageEnteredAt *time.Time `json:"stage_entered_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

type overviewLane struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Terminal bool           `json:"terminal"`
	Count    int64          `json:"count"`
	Value    float64        `json:"value"`
	Cards    []overviewCard `json:"cards"`
}

type overviewZone struct {
	Key   string         `json:"key"`
	Lanes []overviewLane `json:"lanes"`
}

type overviewCompare struct {
	Current  int64 `json:"current"`
	Previous int64 `json:"previous"`
}

type overviewWon struct {
	Current       int64   `json:"current"`
	Previous      int64   `json:"previous"`
	Value         float64 `json:"value"`
	PreviousValue float64 `json:"previous_value"`
}

type overviewOpenPipeline struct {
	Count         int64   `json:"count"`
	Value         float64 `json:"value"`
	WeightedValue float64 `json:"weighted_value"`
}

type overviewSummary struct {
	NewProspects overviewCompare      `json:"new_prospects"`
	NewLeads     overviewCompare      `json:"new_leads"`
	NewDeals     overviewCompare      `json:"new_deals"`
	Won          overviewWon          `json:"won"`
	OpenPipeline overviewOpenPipeline `json:"open_pipeline"`
}

type overviewPeriod struct {
	DateFrom     string `json:"date_from"`
	DateTo       string `json:"date_to"`
	PrevDateFrom string `json:"prev_date_from"`
	PrevDateTo   string `json:"prev_date_to"`
}

type PipelineOverview struct {
	Period  overviewPeriod  `json:"period"`
	Summary overviewSummary `json:"summary"`
	Zones   []overviewZone  `json:"zones"`
}

// window is a half-open [from, to) time range.
type window struct{ from, to time.Time }

// laneDef is one lane of a zone, in display order.
type laneDef struct{ name, kind string }

// overviewEntity describes how one of the three tables maps onto a card, so
// the zone/summary queries below are written once for all three.
type overviewEntity struct {
	table        string
	laneColumn   string // status (prospects/leads) or stage (deals)
	nameColumn   string
	sourceColumn string
	valueExpr    string
	probExpr     string
	lostExpr     string
	fromProspect string
	// laneFilter narrows which rows appear in lanes (not in the summary's
	// "new" counts): a converted Lead is on the board as its Deal instead.
	laneFilter string
}

var (
	prospectEntity = overviewEntity{
		table: "prospects", laneColumn: "status", nameColumn: "name", sourceColumn: "source",
		valueExpr: "0", probExpr: "NULL::int", lostExpr: "NULL::text",
		fromProspect: "false",
	}
	leadEntity = overviewEntity{
		table: "leads", laneColumn: "status", nameColumn: "name", sourceColumn: "source",
		valueExpr: "0", probExpr: "NULL::int", lostExpr: "NULL::text",
		fromProspect: "leads.prospect_id IS NOT NULL",
		laneFilter:   "leads.converted_deal_id IS NULL",
	}
	dealEntity = overviewEntity{
		table: "deals", laneColumn: "stage", nameColumn: "title", sourceColumn: "channel",
		valueExpr: "deals.value", probExpr: "deals.probability", lostExpr: "deals.lost_reason",
		fromProspect: "EXISTS (SELECT 1 FROM leads l WHERE l.id = deals.lead_id AND l.prospect_id IS NOT NULL)",
	}
)

func (e overviewEntity) col(name string) string { return e.table + "." + name }

// base returns a fresh, filtered query on e's table. Every query starts from
// its own base() call so one query's Where clauses never leak into the next.
// The filters mirror the page's filter bar: owner, source (Deal's channel),
// business unit, tag, and a name/company search.
func (e overviewEntity) base(db *gorm.DB, c *fiber.Ctx) *gorm.DB {
	q := db.Table(e.table).Where(e.col("deleted_at") + " IS NULL")
	if v := c.Query("assigned_to"); v != "" {
		if v == "unassigned" {
			q = q.Where(e.col("assigned_to") + " IS NULL")
		} else {
			q = q.Where(e.col("assigned_to")+" = ?", v)
		}
	}
	if v := c.Query("source"); v != "" {
		q = q.Where(e.col(e.sourceColumn)+" = ?", v)
	}
	if v := c.Query("business_unit"); v != "" {
		q = q.Where(e.col("business_unit")+" = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		q = q.Where("? = ANY("+e.col("tags")+")", strings.ToLower(v))
	}
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		like := "%" + v + "%"
		q = q.Where("("+e.col(e.nameColumn)+" ILIKE ? OR EXISTS (SELECT 1 FROM companies sc WHERE sc.id = "+
			e.col("company_id")+" AND sc.name ILIKE ?))", like, like)
	}
	return q
}

// zone loads every lane of one zone in two queries: a grouped count/value per
// lane, and the top `limit` cards per lane via ROW_NUMBER(). Rows count
// toward a lane when they sit in an open lane (any time) or entered a
// terminal lane inside w.
func (e overviewEntity) zone(db *gorm.DB, c *fiber.Ctx, key string, defs []laneDef, w window, limit int) (overviewZone, error) {
	var open, terminal []string
	for _, d := range defs {
		if d.kind == laneOpen {
			open = append(open, d.name)
		} else {
			terminal = append(terminal, d.name)
		}
	}
	lane := e.col(e.laneColumn)
	entered := e.col("stage_entered_at")
	scope := func() *gorm.DB {
		q := e.base(db, c)
		if e.laneFilter != "" {
			q = q.Where(e.laneFilter)
		}
		// GORM renders an empty IN list as IN (NULL), which matches nothing.
		return q.Where(db.Where(lane+" IN ?", open).
			Or(lane+" IN ? AND "+entered+" >= ? AND "+entered+" < ?", terminal, w.from, w.to))
	}

	var aggRows []struct {
		Lane  string
		Count int64
		Value float64
	}
	if err := scope().
		Select(lane + " AS lane, COUNT(*) AS count, COALESCE(SUM(" + e.valueExpr + "), 0) AS value").
		Group(lane).Scan(&aggRows).Error; err != nil {
		return overviewZone{}, err
	}

	cardsByLane := map[string][]overviewCard{}
	if limit > 0 && len(aggRows) > 0 {
		// Open lanes list the longest-waiting cards first (what a reviewer
		// asks about); terminal lanes the most recent closes first.
		ranked := scope().
			Select(lane+" AS lane, "+e.col("id")+", "+e.col(e.nameColumn)+" AS name, "+e.col("company_id")+", "+
				"COALESCE(companies.name, '') AS company_name, "+e.col("assigned_to")+", "+
				e.col(e.sourceColumn)+" AS source, "+e.valueExpr+" AS value, "+
				e.probExpr+" AS probability, "+e.lostExpr+" AS lost_reason, "+
				"("+e.fromProspect+") AS from_prospect, "+entered+", "+e.col("created_at")+", "+
				"ROW_NUMBER() OVER (PARTITION BY "+lane+" ORDER BY "+
				"CASE WHEN "+lane+" IN ? THEN -EXTRACT(EPOCH FROM "+entered+") ELSE EXTRACT(EPOCH FROM "+entered+") END "+
				"NULLS FIRST, "+e.col("id")+") AS rn", terminal).
			Joins("LEFT JOIN companies ON companies.id = " + e.col("company_id"))
		// Card must be a named, exported field: GORM can't populate an
		// embedded unexported type, and would silently leave it zeroed.
		var rows []struct {
			Lane string
			Card overviewCard `gorm:"embedded"`
		}
		if err := db.Table("(?) AS ranked", ranked).Where("rn <= ?", limit).Order("rn").Scan(&rows).Error; err != nil {
			return overviewZone{}, err
		}
		for _, r := range rows {
			cardsByLane[r.Lane] = append(cardsByLane[r.Lane], r.Card)
		}
	}

	aggByLane := map[string]int{}
	for i, r := range aggRows {
		aggByLane[r.Lane] = i
	}
	z := overviewZone{Key: key, Lanes: make([]overviewLane, 0, len(defs))}
	for _, d := range defs {
		l := overviewLane{Name: d.name, Kind: d.kind, Terminal: d.kind != laneOpen, Cards: []overviewCard{}}
		if i, ok := aggByLane[d.name]; ok {
			l.Count, l.Value = aggRows[i].Count, aggRows[i].Value
		}
		if cards := cardsByLane[d.name]; cards != nil {
			l.Cards = cards
		}
		z.Lanes = append(z.Lanes, l)
	}
	return z, nil
}

// compareCounts counts rows whose `column` falls in cur and in prev, in one pass.
func (e overviewEntity) compareCounts(q *gorm.DB, column string, cur, prev window) (overviewCompare, error) {
	col := e.col(column)
	var out overviewCompare
	err := q.Where(col+" >= ? AND "+col+" < ?", prev.from, cur.to).
		Select("COUNT(*) FILTER (WHERE "+col+" >= ? AND "+col+" < ?) AS current, "+
			"COUNT(*) FILTER (WHERE "+col+" >= ? AND "+col+" < ?) AS previous",
			cur.from, cur.to, prev.from, prev.to).
		Scan(&out).Error
	return out, err
}

// resolveOverviewWindow reads date_from/date_to (YYYY-MM-DD, both inclusive)
// and returns the current window plus the equal-length window right before
// it, which the summary strip compares against. Defaults to the last 7 days.
func resolveOverviewWindow(c *fiber.Ctx) (cur, prev window, fields map[string][]string) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	from, to := today.AddDate(0, 0, -6), today
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"date_from", &from}, {"date_to", &to}} {
		if v := c.Query(p.name); v != "" {
			t, err := time.ParseInLocation("2006-01-02", v, time.Local)
			if err != nil {
				return window{}, window{}, map[string][]string{p.name: {"must be a valid YYYY-MM-DD date"}}
			}
			*p.dst = t
		}
	}
	if to.Before(from) {
		return window{}, window{}, map[string][]string{"date_to": {"must be on or after date_from"}}
	}
	cur = window{from: from, to: to.AddDate(0, 0, 1)}
	length := cur.to.Sub(cur.from)
	prev = window{from: cur.from.Add(-length), to: cur.from}
	return cur, prev, nil
}

// Overview — GET /pipeline/overview?date_from=&date_to=&assigned_to=&source=&business_unit=&tag=&search=&card_limit=
// salesPipelineRoles-gated (Admin/Sales Rep/Sales Manager/Marketing).
func (h *PipelineOverviewHandler) Overview(c *fiber.Ctx) error {
	cur, prev, fields := resolveOverviewWindow(c)
	if fields != nil {
		return utils.ValidationError(c, "Invalid date range", fields)
	}
	limit := overviewDefaultCardLimit
	if v := c.Query("card_limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return utils.ValidationError(c, "card_limit is invalid", map[string][]string{"card_limit": {"must be a non-negative integer"}})
		}
		limit = min(n, overviewMaxCardLimit)
	}

	zones, err := h.zones(c, cur, limit)
	if err != nil {
		return utils.Internal(c, "Failed to load pipeline overview")
	}
	summary, err := h.summary(c, cur, prev)
	if err != nil {
		return utils.Internal(c, "Failed to load pipeline overview")
	}

	const day = "2006-01-02"
	return utils.OK(c, PipelineOverview{
		Period: overviewPeriod{
			DateFrom: cur.from.Format(day), DateTo: cur.to.AddDate(0, 0, -1).Format(day),
			PrevDateFrom: prev.from.Format(day), PrevDateTo: prev.to.AddDate(0, 0, -1).Format(day),
		},
		Summary: summary,
		Zones:   zones,
	})
}

func (h *PipelineOverviewHandler) zones(c *fiber.Ctx, w window, limit int) ([]overviewZone, error) {
	// Prospect lanes follow the Admin-configured ProspectStage list, plus the
	// reserved system-set "Converted" status (never a ProspectStage row).
	var pStages []models.ProspectStage
	if err := h.DB.Where("is_active = ?", true).Order("sort_order, id").Find(&pStages).Error; err != nil {
		return nil, err
	}
	pDefs := make([]laneDef, 0, len(pStages)+1)
	for _, s := range pStages {
		kind := laneOpen
		if s.IsDisqualifiedStage {
			kind = laneLost
		}
		pDefs = append(pDefs, laneDef{s.Name, kind})
	}
	pDefs = append(pDefs, laneDef{string(models.ProspectStatusConverted), laneConverted})

	// Lead statuses are a fixed enum.
	lDefs := []laneDef{
		{string(models.LeadStatusNew), laneOpen},
		{string(models.LeadStatusContacted), laneOpen},
		{string(models.LeadStatusQualified), laneOpen},
		{string(models.LeadStatusDisqualified), laneLost},
	}

	var dStages []models.PipelineStage
	if err := h.DB.Where("is_active = ?", true).Order("sort_order, id").Find(&dStages).Error; err != nil {
		return nil, err
	}
	dDefs := make([]laneDef, 0, len(dStages))
	for _, s := range dStages {
		kind := laneOpen
		switch {
		case s.IsWonStage:
			kind = laneWon
		case s.IsLostStage:
			kind = laneLost
		}
		dDefs = append(dDefs, laneDef{s.Name, kind})
	}

	zones := make([]overviewZone, 0, 3)
	for _, z := range []struct {
		key    string
		entity overviewEntity
		defs   []laneDef
	}{
		{"prospect", prospectEntity, pDefs},
		{"lead", leadEntity, lDefs},
		{"deal", dealEntity, dDefs},
	} {
		zone, err := z.entity.zone(h.DB, c, z.key, z.defs, w, limit)
		if err != nil {
			return nil, err
		}
		zones = append(zones, zone)
	}
	return zones, nil
}

func (h *PipelineOverviewHandler) summary(c *fiber.Ctx, cur, prev window) (overviewSummary, error) {
	var s overviewSummary
	var err error
	if s.NewProspects, err = prospectEntity.compareCounts(prospectEntity.base(h.DB, c), "created_at", cur, prev); err != nil {
		return s, err
	}
	if s.NewLeads, err = leadEntity.compareCounts(leadEntity.base(h.DB, c), "created_at", cur, prev); err != nil {
		return s, err
	}
	if s.NewDeals, err = dealEntity.compareCounts(dealEntity.base(h.DB, c), "created_at", cur, prev); err != nil {
		return s, err
	}

	// Won uses deals.status rather than a stage name, so a renamed or custom
	// Won stage still counts (status is kept in sync with the stage's
	// is_won_stage flag on every write — DealHandler.syncStatusWithStageFlags).
	const inCur = "deals.stage_entered_at >= ? AND deals.stage_entered_at < ?"
	if err := dealEntity.base(h.DB, c).
		Where("deals.status = ? AND deals.stage_entered_at >= ? AND deals.stage_entered_at < ?", models.DealStatusWon, prev.from, cur.to).
		Select("COUNT(*) FILTER (WHERE "+inCur+") AS current, "+
			"COUNT(*) FILTER (WHERE "+inCur+") AS previous, "+
			"COALESCE(SUM(deals.value) FILTER (WHERE "+inCur+"), 0) AS value, "+
			"COALESCE(SUM(deals.value) FILTER (WHERE "+inCur+"), 0) AS previous_value",
			cur.from, cur.to, prev.from, prev.to, cur.from, cur.to, prev.from, prev.to).
		Scan(&s.Won).Error; err != nil {
		return s, err
	}

	if err := dealEntity.base(h.DB, c).Where("deals.status = ?", models.DealStatusOpen).
		Select("COUNT(*) AS count, COALESCE(SUM(deals.value), 0) AS value, " +
			"COALESCE(SUM(deals.value * COALESCE(deals.probability, 0) / 100.0), 0) AS weighted_value").
		Scan(&s.OpenPipeline).Error; err != nil {
		return s, err
	}
	return s, nil
}

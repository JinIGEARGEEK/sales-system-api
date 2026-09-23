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

// overviewEntity describes how one of the three tables maps onto a card, so
// the lane/summary queries below are written once for all three.
type overviewEntity struct {
	table        string
	laneColumn   string // status (prospects/leads) or stage (deals)
	nameColumn   string
	sourceColumn string
	valueExpr    string
	probExpr     string
	lostExpr     string
	fromProspect string
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
	}
	dealEntity = overviewEntity{
		table: "deals", laneColumn: "stage", nameColumn: "title", sourceColumn: "channel",
		valueExpr: "deals.value", probExpr: "deals.probability", lostExpr: "deals.lost_reason",
		fromProspect: "EXISTS (SELECT 1 FROM leads l WHERE l.id = deals.lead_id AND l.prospect_id IS NOT NULL)",
	}
)

// base returns a fresh, filtered query on e's table. Every aggregate starts
// from its own base() call so one query's Where clauses never leak into the
// next. The filters mirror the page's filter bar: owner, source (Deal's
// channel), business unit, tag, and a name/company search.
func (e overviewEntity) base(db *gorm.DB, c *fiber.Ctx) *gorm.DB {
	q := db.Table(e.table).Where(e.table + ".deleted_at IS NULL")
	if v := c.Query("assigned_to"); v != "" {
		if v == "unassigned" {
			q = q.Where(e.table + ".assigned_to IS NULL")
		} else {
			q = q.Where(e.table+".assigned_to = ?", v)
		}
	}
	if v := c.Query("source"); v != "" {
		q = q.Where(e.table+"."+e.sourceColumn+" = ?", v)
	}
	if v := c.Query("business_unit"); v != "" {
		q = q.Where(e.table+".business_unit = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		q = q.Where("? = ANY("+e.table+".tags)", strings.ToLower(v))
	}
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		like := "%" + v + "%"
		q = q.Where("("+e.table+"."+e.nameColumn+" ILIKE ? OR EXISTS (SELECT 1 FROM companies sc WHERE sc.id = "+
			e.table+".company_id AND sc.name ILIKE ?))", like, like)
	}
	return q
}

func (e overviewEntity) inLane(q *gorm.DB, lane string) *gorm.DB {
	return q.Where(e.table+"."+e.laneColumn+" = ?", lane)
}

func (e overviewEntity) enteredIn(q *gorm.DB, w window) *gorm.DB {
	return q.Where(e.table+".stage_entered_at >= ? AND "+e.table+".stage_entered_at < ?", w.from, w.to)
}

func (e overviewEntity) createdIn(q *gorm.DB, w window) *gorm.DB {
	return q.Where(e.table+".created_at >= ? AND "+e.table+".created_at < ?", w.from, w.to)
}

func (e overviewEntity) lane(db *gorm.DB, c *fiber.Ctx, name, kind string, w window, limit int, extra func(*gorm.DB) *gorm.DB) (overviewLane, error) {
	terminal := kind != laneOpen
	scope := func() *gorm.DB {
		q := e.inLane(e.base(db, c), name)
		if terminal {
			q = e.enteredIn(q, w)
		}
		if extra != nil {
			q = extra(q)
		}
		return q
	}

	var agg struct {
		Count int64
		Value float64
	}
	if err := scope().Select("COUNT(*) AS count, COALESCE(SUM(" + e.valueExpr + "), 0) AS value").Scan(&agg).Error; err != nil {
		return overviewLane{}, err
	}

	// Open lanes list the longest-waiting cards first (what a reviewer asks
	// about); terminal lanes list the most recent closes first.
	order := e.table + ".stage_entered_at ASC NULLS FIRST, " + e.table + ".id"
	if terminal {
		order = e.table + ".stage_entered_at DESC, " + e.table + ".id DESC"
	}
	cards := []overviewCard{}
	if agg.Count > 0 {
		if err := scope().
			Select(e.table + ".id, " + e.table + "." + e.nameColumn + " AS name, " + e.table + ".company_id, " +
				"COALESCE(companies.name, '') AS company_name, " + e.table + ".assigned_to, " +
				e.table + "." + e.sourceColumn + " AS source, " + e.valueExpr + " AS value, " +
				e.probExpr + " AS probability, " + e.lostExpr + " AS lost_reason, " +
				"(" + e.fromProspect + ") AS from_prospect, " + e.table + ".stage_entered_at, " + e.table + ".created_at").
			Joins("LEFT JOIN companies ON companies.id = " + e.table + ".company_id").
			Order(order).Limit(limit).Scan(&cards).Error; err != nil {
			return overviewLane{}, err
		}
	}
	return overviewLane{Name: name, Kind: kind, Terminal: terminal, Count: agg.Count, Value: agg.Value, Cards: cards}, nil
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
	type laneDef struct{ name, kind string }

	// Prospect lanes follow the Admin-configured ProspectStage list, plus the
	// reserved system-set "Converted" status (never a ProspectStage row).
	var pStages []models.ProspectStage
	if err := h.DB.Where("is_active = ?", true).Order("sort_order, id").Find(&pStages).Error; err != nil {
		return nil, err
	}
	var pDefs []laneDef
	for _, s := range pStages {
		kind := laneOpen
		if s.IsDisqualifiedStage {
			kind = laneLost
		}
		pDefs = append(pDefs, laneDef{s.Name, kind})
	}
	pDefs = append(pDefs, laneDef{string(models.ProspectStatusConverted), laneConverted})

	// Lead statuses are a fixed enum. A converted Lead (converted_deal_id set)
	// is shown as its Deal instead, so it's left out of the Lead lanes rather
	// than counted twice.
	lDefs := []laneDef{
		{string(models.LeadStatusNew), laneOpen},
		{string(models.LeadStatusContacted), laneOpen},
		{string(models.LeadStatusQualified), laneOpen},
		{string(models.LeadStatusDisqualified), laneLost},
	}
	notConverted := func(q *gorm.DB) *gorm.DB { return q.Where("leads.converted_deal_id IS NULL") }

	var dStages []models.PipelineStage
	if err := h.DB.Where("is_active = ?", true).Order("sort_order, id").Find(&dStages).Error; err != nil {
		return nil, err
	}
	var dDefs []laneDef
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

	build := func(key string, e overviewEntity, defs []laneDef, extra func(*gorm.DB) *gorm.DB) (overviewZone, error) {
		z := overviewZone{Key: key, Lanes: []overviewLane{}}
		for _, d := range defs {
			l, err := e.lane(h.DB, c, d.name, d.kind, w, limit, extra)
			if err != nil {
				return z, err
			}
			z.Lanes = append(z.Lanes, l)
		}
		return z, nil
	}

	pz, err := build("prospect", prospectEntity, pDefs, nil)
	if err != nil {
		return nil, err
	}
	lz, err := build("lead", leadEntity, lDefs, notConverted)
	if err != nil {
		return nil, err
	}
	dz, err := build("deal", dealEntity, dDefs, nil)
	if err != nil {
		return nil, err
	}
	return []overviewZone{pz, lz, dz}, nil
}

func (h *PipelineOverviewHandler) summary(c *fiber.Ctx, cur, prev window) (overviewSummary, error) {
	var s overviewSummary
	created := func(e overviewEntity, dst *overviewCompare) error {
		if err := e.createdIn(e.base(h.DB, c), cur).Count(&dst.Current).Error; err != nil {
			return err
		}
		return e.createdIn(e.base(h.DB, c), prev).Count(&dst.Previous).Error
	}
	if err := created(prospectEntity, &s.NewProspects); err != nil {
		return s, err
	}
	if err := created(leadEntity, &s.NewLeads); err != nil {
		return s, err
	}
	if err := created(dealEntity, &s.NewDeals); err != nil {
		return s, err
	}

	// Won uses deals.status rather than a stage name, so a renamed or custom
	// Won stage still counts (status is kept in sync with the stage's
	// is_won_stage flag on every write — DealHandler.syncStatusWithStageFlags).
	type agg struct {
		Count int64
		Value float64
	}
	won := func(w window) (agg, error) {
		var a agg
		err := dealEntity.enteredIn(dealEntity.base(h.DB, c), w).
			Where("deals.status = ?", models.DealStatusWon).
			Select("COUNT(*) AS count, COALESCE(SUM(deals.value), 0) AS value").Scan(&a).Error
		return a, err
	}
	wc, err := won(cur)
	if err != nil {
		return s, err
	}
	wp, err := won(prev)
	if err != nil {
		return s, err
	}
	s.Won = overviewWon{Current: wc.Count, Previous: wp.Count, Value: wc.Value, PreviousValue: wp.Value}

	if err := dealEntity.base(h.DB, c).Where("deals.status = ?", models.DealStatusOpen).
		Select("COUNT(*) AS count, COALESCE(SUM(deals.value), 0) AS value, " +
			"COALESCE(SUM(deals.value * COALESCE(deals.probability, 0) / 100.0), 0) AS weighted_value").
		Scan(&s.OpenPipeline).Error; err != nil {
		return s, err
	}
	return s, nil
}

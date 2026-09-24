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
//   - terminal lanes (Prospect Converted/Disqualified, Lead Disqualified/
//     Converted, Deal Won/Lost) show only records that entered them inside
//     the selected period, or they'd grow forever and bury the review;
//   - an "other" lane (only when non-empty) holds records whose stage isn't
//     one of the zone's lanes, so nothing silently drops off the board.
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

// Lane kinds. "open" and "other" lanes are always current; the rest are
// terminal (only what entered them inside the period).
const (
	laneOpen      = "open"
	laneWon       = "won"
	laneLost      = "lost"
	laneConverted = "converted"
	// laneOther catches records whose stage/status isn't one of the zone's
	// lanes (blank, or a stage an Admin deactivated/renamed), so they stay
	// visible and fixable instead of silently dropping off the board. Only
	// returned when it has records; its Name is "" (the UI labels it).
	laneOther = "other"
)

// isTerminalLane reports whether a lane only shows what entered it inside the
// period (won/lost/converted), as opposed to everything currently in it.
func isTerminalLane(kind string) bool { return kind != laneOpen && kind != laneOther }

// leadConvertedLane is the Lead zone's lane for a Lead that became a Deal.
// Not a real Lead status (conversion leaves status at Qualified); derived
// from converted_deal_id, mirroring Prospect's system-set "Converted".
const leadConvertedLane = "Converted"

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
	// The record's own stage/status value — what an "other" lane card shows,
	// since that lane's name doesn't say.
	Stage string `json:"stage"`
	// The lane it left on its last move ("" if it has never moved), and
	// whether that move went forward or backward in this zone's lane order
	// ("forward" / "backward" / "" when it can't be said, e.g. out of a
	// retired stage or back out of Lost). Computed per zone; see laneRanks.
	PreviousStage string `json:"previous_stage"`
	Direction     string `json:"direction" gorm:"-"`
}

type overviewLane struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Terminal bool   `json:"terminal"`
	// Days a card may sit in this lane before it's stale: the stage's own
	// StaleDays, else models.DefaultStaleDays. 0 on terminal lanes, which
	// are never stale.
	StaleDays int            `json:"stale_days"`
	Count     int64          `json:"count"`
	Value     float64        `json:"value"`
	Cards     []overviewCard `json:"cards"`
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

// overviewHighlight is the board-wide Stale/Moved/Slipped counts, exact over
// every open-lane record (not just the cards returned per lane), so the
// page's highlight buttons and stale-Deals banner never undercount.
type overviewHighlight struct {
	Stale          int64   `json:"stale"`
	Moved          int64   `json:"moved"`
	Slipped        int64   `json:"slipped"`
	StaleDeals     int64   `json:"stale_deals"`
	StaleDealValue float64 `json:"stale_deal_value"`
}

// overviewCohort: of the records created in the period (Cohort), how many
// have reached the next funnel step so far (Converted).
type overviewCohort struct {
	Cohort    int64 `json:"cohort"`
	Converted int64 `json:"converted"`
}

type overviewConversion struct {
	ProspectToLead overviewCohort `json:"prospect_to_lead"`
	LeadToDeal     overviewCohort `json:"lead_to_deal"`
	DealToWon      overviewCohort `json:"deal_to_won"`
}

type overviewSummary struct {
	NewProspects overviewCompare      `json:"new_prospects"`
	NewLeads     overviewCompare      `json:"new_leads"`
	NewDeals     overviewCompare      `json:"new_deals"`
	Won          overviewWon          `json:"won"`
	OpenPipeline overviewOpenPipeline `json:"open_pipeline"`
	Conversion   overviewConversion   `json:"conversion"`
}

type overviewPeriod struct {
	DateFrom     string `json:"date_from"`
	DateTo       string `json:"date_to"`
	PrevDateFrom string `json:"prev_date_from"`
	PrevDateTo   string `json:"prev_date_to"`
}

type PipelineOverview struct {
	Period    overviewPeriod    `json:"period"`
	Summary   overviewSummary   `json:"summary"`
	Highlight overviewHighlight `json:"highlight"`
	Zones     []overviewZone    `json:"zones"`
}

// window is a half-open [from, to) time range.
type window struct{ from, to time.Time }

// laneDef is one lane of a zone, in display order. staleDays is only used on
// open lanes.
type laneDef struct {
	name, kind string
	staleDays  int
}

// Lane ranks give a move its direction: open lanes rank by display order,
// won/converted rank above all of them (so reopening one is a move
// backward), and lost lanes have no rank (leaving Lost is a reopen, neither
// a slip nor progress).
const rankClosedWon = 1 << 20

func laneRanks(defs []laneDef) map[string]int {
	ranks := map[string]int{}
	for i, d := range defs {
		switch d.kind {
		case laneOpen:
			ranks[d.name] = i
		case laneWon, laneConverted:
			ranks[d.name] = rankClosedWon
		}
	}
	return ranks
}

// moveDirection is the direction of a move from `from` to `to`, or "" when
// either end has no rank.
func moveDirection(ranks map[string]int, from, to string) string {
	f, okFrom := ranks[from]
	t, okTo := ranks[to]
	switch {
	case !okFrom || !okTo || f == t:
		return ""
	case t > f:
		return "forward"
	default:
		return "backward"
	}
}

// rankCase renders ranks as a SQL CASE over expr (NULL for no rank), plus
// its bind args, so the Slipped count uses the exact same order as the
// per-card Direction.
func rankCase(expr string, ranks map[string]int) (string, []interface{}) {
	if len(ranks) == 0 {
		return "NULL::int", nil
	}
	var b strings.Builder
	args := make([]interface{}, 0, len(ranks)*2)
	b.WriteString("CASE " + expr)
	for name, r := range ranks {
		b.WriteString(" WHEN ? THEN ?::int")
		args = append(args, name, r)
	}
	b.WriteString(" END")
	return b.String(), args
}

// staleDaysOrDefault resolves a stage's configured threshold.
func staleDaysOrDefault(v *int) int {
	if v != nil && *v > 0 {
		return *v
	}
	return models.DefaultStaleDays
}

// overviewEntity describes how one of the three tables maps onto a card, so
// the zone/summary queries below are written once for all three.
type overviewEntity struct {
	table string
	// laneExpr is the SQL value that decides a row's lane: status
	// (prospects), stage (deals), or for leads, "Converted" once
	// converted_deal_id is set, else status.
	laneExpr     string
	nameColumn   string
	sourceColumn string
	valueExpr    string
	probExpr     string
	lostExpr     string
	fromProspect string
}

var (
	prospectEntity = overviewEntity{
		table: "prospects", laneExpr: "prospects.status", nameColumn: "name", sourceColumn: "source",
		valueExpr: "0", probExpr: "NULL::int", lostExpr: "NULL::text",
		fromProspect: "false",
	}
	leadEntity = overviewEntity{
		table:      "leads",
		laneExpr:   "CASE WHEN leads.converted_deal_id IS NOT NULL THEN '" + leadConvertedLane + "' ELSE leads.status END",
		nameColumn: "name", sourceColumn: "source",
		valueExpr: "0", probExpr: "NULL::int", lostExpr: "NULL::text",
		fromProspect: "leads.prospect_id IS NOT NULL",
	}
	dealEntity = overviewEntity{
		table: "deals", laneExpr: "deals.stage", nameColumn: "title", sourceColumn: "channel",
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

// zone loads every lane of one zone. One subquery tags each row with its
// lane ("bucket"): its own stage when that's one of defs, else "" for the
// "other" lane. Rows are included when they sit in an open lane (any time),
// entered a terminal lane inside w, or belong to no lane at all. From that,
// one grouped query gives each lane's count/value, one ROW_NUMBER() query
// its top `limit` cards, and one more the zone's exact Stale/Moved/Slipped
// counts over all its open-lane rows.
func (e overviewEntity) zone(db *gorm.DB, c *fiber.Ctx, key string, defs []laneDef, w window, limit int) (overviewZone, overviewHighlight, error) {
	var names, open, terminal []string
	for _, d := range defs {
		names = append(names, d.name)
		if isTerminalLane(d.kind) {
			terminal = append(terminal, d.name)
		} else {
			open = append(open, d.name)
		}
	}
	lane := e.laneExpr
	entered := e.col("stage_entered_at")
	laneRank := laneRanks(defs)
	// GORM renders an empty IN list as IN (NULL), which matches nothing.
	rows := e.base(db, c).
		Where(db.Where(lane+" IN ?", open).
			Or(lane+" IN ? AND "+entered+" >= ? AND "+entered+" < ?", terminal, w.from, w.to).
			Or(lane+" IS NULL OR "+lane+" NOT IN ?", names)).
		Joins("LEFT JOIN companies ON companies.id = "+e.col("company_id")).
		Select(e.col("id")+", "+e.col(e.nameColumn)+" AS name, "+e.col("company_id")+", "+
			"COALESCE(companies.name, '') AS company_name, "+e.col("assigned_to")+", "+
			e.col(e.sourceColumn)+" AS source, "+e.valueExpr+" AS value, "+
			e.probExpr+" AS probability, "+e.lostExpr+" AS lost_reason, "+
			"("+e.fromProspect+") AS from_prospect, "+entered+", "+e.col("created_at")+", "+
			"COALESCE("+lane+", '') AS stage, COALESCE("+e.col("previous_stage")+", '') AS previous_stage, "+
			"CASE WHEN "+lane+" IN ? THEN "+lane+" ELSE '' END AS bucket", names)

	var aggRows []struct {
		Lane  string `gorm:"column:bucket"`
		Count int64
		Value float64
	}
	if err := db.Table("(?) AS z", rows).
		Select("bucket, COUNT(*) AS count, COALESCE(SUM(value), 0) AS value").
		Group("bucket").Scan(&aggRows).Error; err != nil {
		return overviewZone{}, overviewHighlight{}, err
	}

	cardsByLane := map[string][]overviewCard{}
	if limit > 0 && len(aggRows) > 0 {
		// Open lanes list the longest-waiting cards first (what a reviewer
		// asks about); terminal lanes the most recent closes first.
		ranked := db.Table("(?) AS z", rows).
			Select("z.*, ROW_NUMBER() OVER (PARTITION BY bucket ORDER BY "+
				"CASE WHEN bucket IN ? THEN -EXTRACT(EPOCH FROM stage_entered_at) ELSE EXTRACT(EPOCH FROM stage_entered_at) END "+
				"NULLS FIRST, id) AS rn", terminal)
		// Card must be a named, exported field: GORM can't populate an
		// embedded unexported type, and would silently leave it zeroed.
		var ranks []struct {
			Lane string       `gorm:"column:bucket"`
			Card overviewCard `gorm:"embedded"`
		}
		if err := db.Table("(?) AS r", ranked).Where("rn <= ?", limit).Order("rn").Scan(&ranks).Error; err != nil {
			return overviewZone{}, overviewHighlight{}, err
		}
		for _, r := range ranks {
			r.Card.Direction = moveDirection(laneRank, r.Card.PreviousStage, r.Card.Stage)
			cardsByLane[r.Lane] = append(cardsByLane[r.Lane], r.Card)
		}
	}

	hl, err := zoneHighlight(db, rows, defs, laneRank, w)
	if err != nil {
		return overviewZone{}, overviewHighlight{}, err
	}

	aggByLane := map[string]int{}
	for i, r := range aggRows {
		aggByLane[r.Lane] = i
	}
	build := func(name, kind string, staleDays int) overviewLane {
		l := overviewLane{Name: name, Kind: kind, Terminal: isTerminalLane(kind), Cards: []overviewCard{}}
		if !l.Terminal {
			l.StaleDays = staleDays
		}
		if i, ok := aggByLane[name]; ok {
			l.Count, l.Value = aggRows[i].Count, aggRows[i].Value
		}
		if cards := cardsByLane[name]; cards != nil {
			l.Cards = cards
		}
		return l
	}
	z := overviewZone{Key: key, Lanes: make([]overviewLane, 0, len(defs)+1)}
	for _, d := range defs {
		z.Lanes = append(z.Lanes, build(d.name, d.kind, d.staleDays))
	}
	if _, ok := aggByLane[""]; ok {
		z.Lanes = append(z.Lanes, build("", laneOther, models.DefaultStaleDays))
	}
	return z, hl, nil
}

// zoneHighlight counts a zone's open-lane rows (open lanes plus "other")
// that are stale (past their lane's threshold), moved (changed lane inside
// w, not merely created there), and slipped (moved backward). The stale
// cutoff is local midnight minus the threshold, so "stale" means the same
// whole calendar days the page shows on each card.
func zoneHighlight(db *gorm.DB, rows *gorm.DB, defs []laneDef, ranks map[string]int, w window) (overviewHighlight, error) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	cutoffOf := func(days int) time.Time { return midnight.AddDate(0, 0, -days) }

	var terminal []string
	var cutoff strings.Builder
	cutoffArgs := []interface{}{}
	cutoff.WriteString("CASE bucket")
	for _, d := range defs {
		if isTerminalLane(d.kind) {
			terminal = append(terminal, d.name)
			continue
		}
		cutoff.WriteString(" WHEN ? THEN ?::timestamptz")
		cutoffArgs = append(cutoffArgs, d.name, cutoffOf(d.staleDays))
	}
	cutoff.WriteString(" ELSE ?::timestamptz END")
	cutoffArgs = append(cutoffArgs, cutoffOf(models.DefaultStaleDays))

	curRank, curArgs := rankCase("stage", ranks)
	prevRank, prevArgs := rankCase("previous_stage", ranks)

	stale := "COALESCE(stage_entered_at, created_at) < " + cutoff.String()
	moved := "stage_entered_at >= ? AND stage_entered_at < ? AND stage_entered_at > created_at + interval '1 minute'"
	slipped := moved + " AND (" + prevRank + ") > (" + curRank + ")"

	args := []interface{}{}
	args = append(args, cutoffArgs...)
	args = append(args, w.from, w.to)
	args = append(args, w.from, w.to)
	args = append(args, prevArgs...)
	args = append(args, curArgs...)
	args = append(args, cutoffArgs...)

	var hl struct {
		Stale      int64
		Moved      int64
		Slipped    int64
		StaleValue float64
	}
	err := db.Table("(?) AS z", rows).Where("bucket NOT IN ?", terminal).
		Select("COUNT(*) FILTER (WHERE "+stale+") AS stale, "+
			"COUNT(*) FILTER (WHERE "+moved+") AS moved, "+
			"COUNT(*) FILTER (WHERE "+slipped+") AS slipped, "+
			"COALESCE(SUM(value) FILTER (WHERE "+stale+"), 0) AS stale_value", args...).
		Scan(&hl).Error
	if err != nil {
		return overviewHighlight{}, err
	}
	return overviewHighlight{Stale: hl.Stale, Moved: hl.Moved, Slipped: hl.Slipped, StaleDealValue: hl.StaleValue}, nil
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

	zones, highlight, err := h.zones(c, cur, limit)
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
		Summary:   summary,
		Highlight: highlight,
		Zones:     zones,
	})
}

func (h *PipelineOverviewHandler) zones(c *fiber.Ctx, w window, limit int) ([]overviewZone, overviewHighlight, error) {
	// Prospect lanes follow the Admin-configured ProspectStage list, plus the
	// reserved system-set "Converted" status (never a ProspectStage row).
	var pStages []models.ProspectStage
	if err := h.DB.Where("is_active = ?", true).Order("sort_order, id").Find(&pStages).Error; err != nil {
		return nil, overviewHighlight{}, err
	}
	pDefs := make([]laneDef, 0, len(pStages)+1)
	for _, s := range pStages {
		kind := laneOpen
		if s.IsDisqualifiedStage {
			kind = laneLost
		}
		pDefs = append(pDefs, laneDef{s.Name, kind, staleDaysOrDefault(s.StaleDays)})
	}
	pDefs = append(pDefs, laneDef{string(models.ProspectStatusConverted), laneConverted, 0})

	// Lead statuses are a fixed enum, plus the derived Converted lane (a Lead
	// that became a Deal, shown here for the period it converted in, the same
	// way Prospect's Converted lane works one stage earlier). No config row
	// to hold a threshold, so they use the default.
	lDefs := []laneDef{
		{string(models.LeadStatusNew), laneOpen, models.DefaultStaleDays},
		{string(models.LeadStatusContacted), laneOpen, models.DefaultStaleDays},
		{string(models.LeadStatusQualified), laneOpen, models.DefaultStaleDays},
		{string(models.LeadStatusDisqualified), laneLost, 0},
		{leadConvertedLane, laneConverted, 0},
	}

	var dStages []models.PipelineStage
	if err := h.DB.Where("is_active = ?", true).Order("sort_order, id").Find(&dStages).Error; err != nil {
		return nil, overviewHighlight{}, err
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
		dDefs = append(dDefs, laneDef{s.Name, kind, staleDaysOrDefault(s.StaleDays)})
	}

	zones := make([]overviewZone, 0, 3)
	var total overviewHighlight
	for _, z := range []struct {
		key    string
		entity overviewEntity
		defs   []laneDef
	}{
		{"prospect", prospectEntity, pDefs},
		{"lead", leadEntity, lDefs},
		{"deal", dealEntity, dDefs},
	} {
		zone, hl, err := z.entity.zone(h.DB, c, z.key, z.defs, w, limit)
		if err != nil {
			return nil, overviewHighlight{}, err
		}
		zones = append(zones, zone)
		total.Stale += hl.Stale
		total.Moved += hl.Moved
		total.Slipped += hl.Slipped
		if z.key == "deal" {
			total.StaleDeals, total.StaleDealValue = hl.Stale, hl.StaleDealValue
		}
	}
	return zones, total, nil
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

	// Cohort conversion: of the records created in the period, how many
	// have reached the next step so far. Unlike comparing two "new" counts,
	// this can't exceed 100%.
	for _, co := range []struct {
		entity    overviewEntity
		converted string
		dst       *overviewCohort
	}{
		{prospectEntity, "prospects.converted_lead_id IS NOT NULL", &s.Conversion.ProspectToLead},
		{leadEntity, "leads.converted_deal_id IS NOT NULL", &s.Conversion.LeadToDeal},
		{dealEntity, "deals.status = 'won'", &s.Conversion.DealToWon},
	} {
		created := co.entity.col("created_at")
		if err := co.entity.base(h.DB, c).
			Where(created+" >= ? AND "+created+" < ?", cur.from, cur.to).
			Select("COUNT(*) AS cohort, COUNT(*) FILTER (WHERE " + co.converted + ") AS converted").
			Scan(co.dst).Error; err != nil {
			return s, err
		}
	}

	if err := dealEntity.base(h.DB, c).Where("deals.status = ?", models.DealStatusOpen).
		Select("COUNT(*) AS count, COALESCE(SUM(deals.value), 0) AS value, " +
			"COALESCE(SUM(deals.value * COALESCE(deals.probability, 0) / 100.0), 0) AS weighted_value").
		Scan(&s.OpenPipeline).Error; err != nil {
		return s, err
	}
	return s, nil
}

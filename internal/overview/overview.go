// Package overview computes the Overview Pipeline payload (FR-CRM-123) — the
// board behind GET /pipeline/overview (handlers.PipelineOverviewHandler) and
// the weekly digest email (notifier), so both always tell the same story. It
// knows nothing about HTTP: callers pass Filters and Windows as values.
//
// The payload behind the Overview Pipeline page: Prospect, Lead and Deal lanes on a
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
package overview

import (
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// Filters mirror the page's filter bar; empty fields don't filter.
type Filters struct {
	AssignedTo   string // user id, or "unassigned"
	Source       string // Prospect/Lead source, Deal channel
	BusinessUnit string
	Tag          string
	Search       string // record name/title or linked Company name
}

// Build computes the whole payload for window cur (compared against prev),
// returning up to `limit` cards per lane.
func Build(db *gorm.DB, f Filters, cur, prev Window, limit int) (PipelineOverview, error) {
	zones, highlight, err := buildZones(db, f, cur, limit)
	if err != nil {
		return PipelineOverview{}, err
	}
	summary, err := buildSummary(db, f, cur, prev)
	if err != nil {
		return PipelineOverview{}, err
	}
	const day = "2006-01-02"
	return PipelineOverview{
		Period: Period{
			DateFrom: cur.From.Format(day), DateTo: cur.To.AddDate(0, 0, -1).Format(day),
			PrevDateFrom: prev.From.Format(day), PrevDateTo: prev.To.AddDate(0, 0, -1).Format(day),
		},
		Summary:   summary,
		Highlight: highlight,
		Zones:     zones,
	}, nil
}

// PreviousWindow is the equal-length window right before cur, which the
// summary compares against.
func PreviousWindow(cur Window) Window {
	length := cur.To.Sub(cur.From)
	return Window{From: cur.From.Add(-length), To: cur.From}
}

const (
	DefaultCardLimit = 30
	MaxCardLimit     = 100
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

type Card struct {
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

type Lane struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Terminal bool   `json:"terminal"`
	// Days a card may sit in this lane before it's stale: the stage's own
	// StaleDays, else models.DefaultStaleDays. 0 on terminal lanes, which
	// are never stale.
	StaleDays int     `json:"stale_days"`
	Count     int64   `json:"count"`
	Value     float64 `json:"value"`
	Cards     []Card  `json:"cards"`
}

type Zone struct {
	Key   string `json:"key"`
	Lanes []Lane `json:"lanes"`
}

type Compare struct {
	Current  int64 `json:"current"`
	Previous int64 `json:"previous"`
}

type Won struct {
	Current       int64   `json:"current"`
	Previous      int64   `json:"previous"`
	Value         float64 `json:"value"`
	PreviousValue float64 `json:"previous_value"`
}

type OpenPipeline struct {
	Count         int64   `json:"count"`
	Value         float64 `json:"value"`
	WeightedValue float64 `json:"weighted_value"`
}

// Highlight is the board-wide Stale/Moved/Slipped counts, exact over
// every open-lane record (not just the cards returned per lane), so the
// page's highlight buttons and stale-Deals banner never undercount.
type Highlight struct {
	Stale          int64   `json:"stale"`
	Moved          int64   `json:"moved"`
	Slipped        int64   `json:"slipped"`
	StaleDeals     int64   `json:"stale_deals"`
	StaleDealValue float64 `json:"stale_deal_value"`
}

// Cohort: of the records created in the period (Cohort), how many
// have reached the next funnel step so far (Converted).
type Cohort struct {
	Cohort    int64 `json:"cohort"`
	Converted int64 `json:"converted"`
}

type Conversion struct {
	ProspectToLead Cohort `json:"prospect_to_lead"`
	LeadToDeal     Cohort `json:"lead_to_deal"`
	DealToWon      Cohort `json:"deal_to_won"`
}

type Summary struct {
	NewProspects Compare      `json:"new_prospects"`
	NewLeads     Compare      `json:"new_leads"`
	NewDeals     Compare      `json:"new_deals"`
	Won          Won          `json:"won"`
	OpenPipeline OpenPipeline `json:"open_pipeline"`
	Conversion   Conversion   `json:"conversion"`
}

type Period struct {
	DateFrom     string `json:"date_from"`
	DateTo       string `json:"date_to"`
	PrevDateFrom string `json:"prev_date_from"`
	PrevDateTo   string `json:"prev_date_to"`
}

type PipelineOverview struct {
	Period    Period    `json:"period"`
	Summary   Summary   `json:"summary"`
	Highlight Highlight `json:"highlight"`
	Zones     []Zone    `json:"zones"`
}

// Window is a half-open [From, To) time range.
type Window struct{ From, To time.Time }

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
func (e overviewEntity) base(db *gorm.DB, f Filters) *gorm.DB {
	q := db.Table(e.table).Where(e.col("deleted_at") + " IS NULL")
	if v := f.AssignedTo; v != "" {
		if v == "unassigned" {
			q = q.Where(e.col("assigned_to") + " IS NULL")
		} else {
			q = q.Where(e.col("assigned_to")+" = ?", v)
		}
	}
	if v := f.Source; v != "" {
		q = q.Where(e.col(e.sourceColumn)+" = ?", v)
	}
	if v := f.BusinessUnit; v != "" {
		q = q.Where(e.col("business_unit")+" = ?", v)
	}
	if v := f.Tag; v != "" {
		q = q.Where("? = ANY("+e.col("tags")+")", strings.ToLower(v))
	}
	if v := strings.TrimSpace(f.Search); v != "" {
		like := utils.LikePattern(v)
		q = q.Where("("+e.col(e.nameColumn)+" ILIKE ? ESCAPE '\\' OR EXISTS (SELECT 1 FROM companies sc WHERE sc.id = "+
			e.col("company_id")+" AND sc.name ILIKE ? ESCAPE '\\'))", like, like)
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
func (e overviewEntity) zone(db *gorm.DB, f Filters, key string, defs []laneDef, w Window, limit int) (Zone, Highlight, error) {
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
	rows := e.base(db, f).
		Where(db.Where(lane+" IN ?", open).
			Or(lane+" IN ? AND "+entered+" >= ? AND "+entered+" < ?", terminal, w.From, w.To).
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
		return Zone{}, Highlight{}, err
	}

	cardsByLane := map[string][]Card{}
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
			Lane string `gorm:"column:bucket"`
			Card Card   `gorm:"embedded"`
		}
		if err := db.Table("(?) AS r", ranked).Where("rn <= ?", limit).Order("rn").Scan(&ranks).Error; err != nil {
			return Zone{}, Highlight{}, err
		}
		for _, r := range ranks {
			r.Card.Direction = moveDirection(laneRank, r.Card.PreviousStage, r.Card.Stage)
			cardsByLane[r.Lane] = append(cardsByLane[r.Lane], r.Card)
		}
	}

	hl, err := zoneHighlight(db, rows, defs, laneRank, w)
	if err != nil {
		return Zone{}, Highlight{}, err
	}

	aggByLane := map[string]int{}
	for i, r := range aggRows {
		aggByLane[r.Lane] = i
	}
	build := func(name, kind string, staleDays int) Lane {
		l := Lane{Name: name, Kind: kind, Terminal: isTerminalLane(kind), Cards: []Card{}}
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
	z := Zone{Key: key, Lanes: make([]Lane, 0, len(defs)+1)}
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
func zoneHighlight(db *gorm.DB, rows *gorm.DB, defs []laneDef, ranks map[string]int, w Window) (Highlight, error) {
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
	args = append(args, w.From, w.To)
	args = append(args, w.From, w.To)
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
		return Highlight{}, err
	}
	return Highlight{Stale: hl.Stale, Moved: hl.Moved, Slipped: hl.Slipped, StaleDealValue: hl.StaleValue}, nil
}

// compareCounts counts rows whose `column` falls in cur and in prev, in one pass.
func (e overviewEntity) compareCounts(q *gorm.DB, column string, cur, prev Window) (Compare, error) {
	col := e.col(column)
	var out Compare
	err := q.Where(col+" >= ? AND "+col+" < ?", prev.From, cur.To).
		Select("COUNT(*) FILTER (WHERE "+col+" >= ? AND "+col+" < ?) AS current, "+
			"COUNT(*) FILTER (WHERE "+col+" >= ? AND "+col+" < ?) AS previous",
			cur.From, cur.To, prev.From, prev.To).
		Scan(&out).Error
	return out, err
}

func buildZones(db *gorm.DB, f Filters, w Window, limit int) ([]Zone, Highlight, error) {
	// Prospect lanes follow the Admin-configured ProspectStage list, plus the
	// reserved system-set "Converted" status (never a ProspectStage row).
	var pStages []models.ProspectStage
	if err := db.Where("is_active = ?", true).Order("sort_order, id").Find(&pStages).Error; err != nil {
		return nil, Highlight{}, err
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
	if err := db.Where("is_active = ?", true).Order("sort_order, id").Find(&dStages).Error; err != nil {
		return nil, Highlight{}, err
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

	zones := make([]Zone, 0, 3)
	var total Highlight
	for _, z := range []struct {
		key    string
		entity overviewEntity
		defs   []laneDef
	}{
		{"prospect", prospectEntity, pDefs},
		{"lead", leadEntity, lDefs},
		{"deal", dealEntity, dDefs},
	} {
		zone, hl, err := z.entity.zone(db, f, z.key, z.defs, w, limit)
		if err != nil {
			return nil, Highlight{}, err
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

func buildSummary(db *gorm.DB, f Filters, cur, prev Window) (Summary, error) {
	var s Summary
	var err error
	if s.NewProspects, err = prospectEntity.compareCounts(prospectEntity.base(db, f), "created_at", cur, prev); err != nil {
		return s, err
	}
	if s.NewLeads, err = leadEntity.compareCounts(leadEntity.base(db, f), "created_at", cur, prev); err != nil {
		return s, err
	}
	if s.NewDeals, err = dealEntity.compareCounts(dealEntity.base(db, f), "created_at", cur, prev); err != nil {
		return s, err
	}

	// Won uses deals.status rather than a stage name, so a renamed or custom
	// Won stage still counts (status is kept in sync with the stage's
	// is_won_stage flag on every write — DealHandler.syncStatusWithStageFlags).
	const inCur = "deals.stage_entered_at >= ? AND deals.stage_entered_at < ?"
	if err := dealEntity.base(db, f).
		Where("deals.status = ? AND deals.stage_entered_at >= ? AND deals.stage_entered_at < ?", models.DealStatusWon, prev.From, cur.To).
		Select("COUNT(*) FILTER (WHERE "+inCur+") AS current, "+
			"COUNT(*) FILTER (WHERE "+inCur+") AS previous, "+
			"COALESCE(SUM(deals.value) FILTER (WHERE "+inCur+"), 0) AS value, "+
			"COALESCE(SUM(deals.value) FILTER (WHERE "+inCur+"), 0) AS previous_value",
			cur.From, cur.To, prev.From, prev.To, cur.From, cur.To, prev.From, prev.To).
		Scan(&s.Won).Error; err != nil {
		return s, err
	}

	// Cohort conversion: of the records created in the period, how many
	// have reached the next step so far. Unlike comparing two "new" counts,
	// this can't exceed 100%.
	for _, co := range []struct {
		entity    overviewEntity
		converted string
		dst       *Cohort
	}{
		{prospectEntity, "prospects.converted_lead_id IS NOT NULL", &s.Conversion.ProspectToLead},
		{leadEntity, "leads.converted_deal_id IS NOT NULL", &s.Conversion.LeadToDeal},
		{dealEntity, "deals.status = 'won'", &s.Conversion.DealToWon},
	} {
		created := co.entity.col("created_at")
		if err := co.entity.base(db, f).
			Where(created+" >= ? AND "+created+" < ?", cur.From, cur.To).
			Select("COUNT(*) AS cohort, COUNT(*) FILTER (WHERE " + co.converted + ") AS converted").
			Scan(co.dst).Error; err != nil {
			return s, err
		}
	}

	if err := dealEntity.base(db, f).Where("deals.status = ?", models.DealStatusOpen).
		Select("COUNT(*) AS count, COALESCE(SUM(deals.value), 0) AS value, " +
			"COALESCE(SUM(deals.value * COALESCE(deals.probability, 0) / 100.0), 0) AS weighted_value").
		Scan(&s.OpenPipeline).Error; err != nil {
		return s, err
	}
	return s, nil
}

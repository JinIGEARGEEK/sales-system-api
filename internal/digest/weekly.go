// Package digest builds and sends the weekly Overview Pipeline email to
// Admins and Sales Managers (FR-CRM-123): last week's summary, what needs a
// question (stale Deals, slips, losses), and a link to the board. Built from
// internal/overview, the same code as GET /pipeline/overview, so the email
// and the page always agree. Its own package so both the scheduler
// (notifier.StartWeeklyDigest) and the Admin preview/test endpoints
// (handlers) can use it without an import cycle.
package digest

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/overview"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const (
	// weeklyDigestSendHour is the local hour on Monday from which the digest
	// is due; the hourly check sends it on the first tick at or after it.
	weeklyDigestSendHour = 8
	// digestListLimit caps each list (stale, slipped, lost) in the email.
	digestListLimit = 10
	// digestCardScan is how many cards per lane the digest reads to pick
	// those lists from. Open lanes rank longest-waiting first, so a Deal that
	// slipped back this week sits at the end of its lane — reading only the
	// top digestListLimit would leave "Slipped back" empty on a busy lane.
	digestCardScan = 500
)

// SendMail delivers one digest email. A variable so tests can capture sends
// instead of dialing SMTP; production code never reassigns it.
var SendMail = utils.SendMail

// Weekly is one rendered digest: what would be (or was) sent, to whom,
// for which week.
type Weekly struct {
	Subject    string
	Body       string
	Recipients []string
	Week       overview.Window
}

// weekStart is local midnight on the Monday of t's week.
func weekStart(t time.Time) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
	return day.AddDate(0, 0, -offset)
}

// MaybeSendWeekly sends last week's digest if it's due: SMTP is
// configured, the digest is enabled, it's past Monday 08:00 local, and it
// hasn't gone out since this Monday began. If the server was down Monday
// morning, the next check catches up. Called hourly by
// notifier.StartWeeklyDigest.
func MaybeSendWeekly(db *gorm.DB, cfg *config.Config, now time.Time) error {
	if cfg == nil || cfg.SMTPHost == "" {
		return nil
	}
	var settings models.AppSettings
	if err := db.First(&settings).Error; err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	monday := weekStart(now)
	if !settings.WeeklyDigestEnabled || now.Before(monday.Add(weeklyDigestSendHour*time.Hour)) {
		return nil
	}
	// Claim the week before sending, in one conditional UPDATE, so two API
	// instances (or an overlapping check) can't both send it. Released
	// again below if nothing could be sent, so the next check retries.
	claim := db.Model(&models.AppSettings{}).Where("id = ?", settings.ID).
		Where("last_weekly_digest_at IS NULL OR last_weekly_digest_at < ?", monday).
		UpdateColumn("last_weekly_digest_at", now)
	if claim.Error != nil {
		return fmt.Errorf("claim week: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		return nil
	}
	release := func() error {
		return db.Model(&models.AppSettings{}).Where("id = ?", settings.ID).
			UpdateColumn("last_weekly_digest_at", settings.LastWeeklyDigestAt).Error
	}

	digest, err := BuildWeekly(db, cfg, now)
	if err != nil {
		if rerr := release(); rerr != nil {
			log.Printf("weekly digest: release claim: %v", rerr)
		}
		return err
	}
	sent := 0
	for _, to := range digest.Recipients {
		if err := SendMail(cfg, to, digest.Subject, digest.Body); err != nil {
			log.Printf("weekly digest: send to %s: %v", to, err)
			continue
		}
		sent++
	}
	// The week stays claimed unless every send failed (then retry next
	// hour). No recipients at all also counts as done.
	if len(digest.Recipients) > 0 && sent == 0 {
		if err := release(); err != nil {
			return fmt.Errorf("every send failed; release claim: %w", err)
		}
		return fmt.Errorf("every send failed; will retry")
	}
	return nil
}

// BuildWeekly renders the digest for the full week (Monday–Sunday)
// before now's week, compared with the week before that.
func BuildWeekly(db *gorm.DB, cfg *config.Config, now time.Time) (Weekly, error) {
	monday := weekStart(now)
	week := overview.Window{From: monday.AddDate(0, 0, -7), To: monday}
	ov, err := overview.Build(db, overview.Filters{}, week, overview.PreviousWindow(week), digestCardScan)
	if err != nil {
		return Weekly{}, fmt.Errorf("build overview: %w", err)
	}

	var recipients []models.User
	if err := db.Where("role IN ? AND is_active = ? AND email <> ''", []models.Role{models.RoleAdmin, models.RoleSalesManager}, true).
		Order("id").Find(&recipients).Error; err != nil {
		return Weekly{}, fmt.Errorf("load recipients: %w", err)
	}
	emails := make([]string, 0, len(recipients))
	for _, u := range recipients {
		emails = append(emails, u.Email)
	}

	names, err := ownerNames(db, ov)
	if err != nil {
		return Weekly{}, err
	}
	appURL := ""
	if cfg != nil {
		appURL = cfg.AppURL
	}
	last := week.To.AddDate(0, 0, -1)
	return Weekly{
		Subject:    fmt.Sprintf("Weekly pipeline review: %s - %s", week.From.Format("2 Jan"), last.Format("2 Jan 2006")),
		Body:       renderDigest(ov, week, names, appURL, now),
		Recipients: emails,
		Week:       week,
	}, nil
}

// ownerNames resolves every card owner in the payload to "First Last".
func ownerNames(db *gorm.DB, ov overview.PipelineOverview) (map[uint]string, error) {
	ids := map[uint]bool{}
	for _, z := range ov.Zones {
		for _, l := range z.Lanes {
			for _, c := range l.Cards {
				if c.AssignedTo != nil {
					ids[*c.AssignedTo] = true
				}
			}
		}
	}
	names := map[uint]string{}
	if len(ids) == 0 {
		return names, nil
	}
	list := make([]uint, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var users []models.User
	if err := db.Unscoped().Where("id IN ?", list).Find(&users).Error; err != nil {
		return nil, fmt.Errorf("load owners: %w", err)
	}
	for _, u := range users {
		names[u.ID] = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	return names, nil
}

var lostReasonLabels = map[string]string{
	"price": "Price", "timing": "Timing", "competitor": "Competitor", "no_budget": "No budget", "other": "Other",
}

// baht formats a value compactly with at most one decimal (฿850K, ฿1.6K,
// ฿4.7M), like the page's priceFormatCompact ('0,0.[0]a').
func baht(v float64) string {
	oneDecimal := func(x float64) string {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", math.Round(x*10)/10), ".0")
	}
	// Decide the unit on the rounded thousands figure, so ฿999,960 (which
	// rounds to 1,000.0K) reads ฿1M rather than ฿1000K.
	switch k := math.Round(v/100) / 10; {
	case k >= 1000:
		return "฿" + oneDecimal(v/1e6) + "M"
	case k >= 1:
		return "฿" + oneDecimal(v/1e3) + "K"
	default:
		return fmt.Sprintf("฿%.0f", v)
	}
}

func signed(n int64) string {
	if n > 0 {
		return fmt.Sprintf("+%d", n)
	}
	return fmt.Sprintf("%d", n)
}

// cohortLine reads e.g. "2 of 7 new Prospects became Leads (29%)"; verb is
// the rest of the sentence ("became Leads", "were Won").
func cohortLine(c overview.Cohort, from, verb string) string {
	if c.Cohort == 0 {
		return ""
	}
	pct := int64(math.Round(float64(c.Converted) * 100 / float64(c.Cohort))) // rounded, as on the page
	return fmt.Sprintf("   %d of %d new %s %s (%d%%)", c.Converted, c.Cohort, from, verb, pct)
}

// daysIn is whole calendar days since the card entered its lane.
func daysIn(c overview.Card, now time.Time) int {
	entered := c.CreatedAt
	if c.StageEnteredAt != nil {
		entered = *c.StageEnteredAt
	}
	d0 := time.Date(entered.Year(), entered.Month(), entered.Day(), 0, 0, 0, 0, time.Local)
	d1 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return int(d1.Sub(d0).Hours() / 24)
}

type digestRow struct {
	card overview.Card
	lane string
	days int
}

func renderDigest(ov overview.PipelineOverview, week overview.Window, names map[uint]string, appURL string, now time.Time) string {
	s := ov.Summary
	var b strings.Builder
	line := func(format string, a ...interface{}) { fmt.Fprintf(&b, format+"\n", a...) }
	owner := func(c overview.Card) string {
		if c.AssignedTo == nil {
			return "Unassigned"
		}
		if n := names[*c.AssignedTo]; n != "" {
			return n
		}
		return "Unassigned"
	}

	line("Weekly pipeline review: %s - %s", week.From.Format("Mon 2 Jan"), week.To.AddDate(0, 0, -1).Format("Mon 2 Jan 2006"))
	line("Compared with the week before.")
	line("")
	line("SUMMARY")
	line("  New Prospects   %d (%s)", s.NewProspects.Current, signed(s.NewProspects.Current-s.NewProspects.Previous))
	line("  New Leads       %d (%s)%s", s.NewLeads.Current, signed(s.NewLeads.Current-s.NewLeads.Previous), cohortLine(s.Conversion.ProspectToLead, "Prospects", "became Leads"))
	line("  New Deals       %d (%s)%s", s.NewDeals.Current, signed(s.NewDeals.Current-s.NewDeals.Previous), cohortLine(s.Conversion.LeadToDeal, "Leads", "became Deals"))
	line("  Won             %d, %s (%s)%s", s.Won.Current, baht(s.Won.Value), signed(s.Won.Current-s.Won.Previous), cohortLine(s.Conversion.DealToWon, "Deals", "were Won"))
	line("  Open pipeline   %s across %d deals, weighted %s", baht(s.OpenPipeline.Value), s.OpenPipeline.Count, baht(s.OpenPipeline.WeightedValue))
	line("")

	var stale, slipped, lost []digestRow
	for _, z := range ov.Zones {
		if z.Key != "deal" {
			continue
		}
		for _, l := range z.Lanes {
			for _, c := range l.Cards {
				row := digestRow{card: c, lane: l.Name, days: daysIn(c, now)}
				switch {
				case l.Kind == "lost":
					lost = append(lost, row)
				case !l.Terminal:
					if l.StaleDays > 0 && row.days > l.StaleDays {
						stale = append(stale, row)
					}
					entered := c.CreatedAt
					if c.StageEnteredAt != nil {
						entered = *c.StageEnteredAt
					}
					if c.Direction == "backward" && !entered.Before(week.From) && entered.Before(week.To) {
						slipped = append(slipped, row)
					}
				}
			}
		}
	}
	sort.SliceStable(stale, func(i, j int) bool { return stale[i].days > stale[j].days })
	limitRows := func(rows []digestRow) []digestRow {
		if len(rows) > digestListLimit {
			return rows[:digestListLimit]
		}
		return rows
	}

	h := ov.Highlight
	line("NEEDS ATTENTION")
	if h.StaleDeals == 0 && h.Slipped == 0 && len(lost) == 0 {
		line("  Nothing stale, slipped or lost. Nice week.")
	}
	if h.StaleDeals > 0 {
		line("  %d open deals worth %s are past their stage's stale limit. Longest waiting:", h.StaleDeals, baht(h.StaleDealValue))
		for _, r := range limitRows(stale) {
			line("    - %s (%s): %s for %d days, %s, %s", r.card.Name, r.card.CompanyName, r.lane, r.days, baht(r.card.Value), owner(r.card))
		}
	}
	if len(slipped) > 0 {
		line("  Slipped back this week:")
		for _, r := range limitRows(slipped) {
			line("    - %s (%s): %s -> %s, %s, %s", r.card.Name, r.card.CompanyName, r.card.PreviousStage, r.lane, baht(r.card.Value), owner(r.card))
		}
	}
	if len(lost) > 0 {
		line("  Lost this week:")
		for _, r := range limitRows(lost) {
			reason := "no reason recorded"
			if r.card.LostReason != nil && *r.card.LostReason != "" {
				reason = lostReasonLabels[*r.card.LostReason]
				if reason == "" {
					reason = *r.card.LostReason
				}
			}
			line("    - %s (%s): %s, %s", r.card.Name, r.card.CompanyName, baht(r.card.Value), reason)
		}
	}
	line("  Moved this week: %d, of which slipped back: %d. Stale across all stages: %d.", h.Moved, h.Slipped, h.Stale)
	line("")
	if appURL != "" {
		line("Open the Overview Pipeline for this week:")
		line("%s/crm/overview-pipeline?period=lastWeek", strings.TrimRight(appURL, "/"))
		line("")
	}
	line("You get this because you're an Admin or Sales Manager. An Admin can turn it off in CRM Settings.")
	return b.String()
}

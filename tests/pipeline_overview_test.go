package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

type overviewResp struct {
	Data struct {
		Period struct {
			DateFrom     string `json:"date_from"`
			DateTo       string `json:"date_to"`
			PrevDateFrom string `json:"prev_date_from"`
			PrevDateTo   string `json:"prev_date_to"`
		} `json:"period"`
		Summary struct {
			NewProspects struct{ Current, Previous int64 } `json:"new_prospects"`
			NewLeads     struct{ Current, Previous int64 } `json:"new_leads"`
			NewDeals     struct{ Current, Previous int64 } `json:"new_deals"`
			Won          struct {
				Current  int64   `json:"current"`
				Previous int64   `json:"previous"`
				Value    float64 `json:"value"`
			} `json:"won"`
			OpenPipeline struct {
				Count         int64   `json:"count"`
				Value         float64 `json:"value"`
				WeightedValue float64 `json:"weighted_value"`
			} `json:"open_pipeline"`
		} `json:"summary"`
		Zones []struct {
			Key   string `json:"key"`
			Lanes []struct {
				Name     string  `json:"name"`
				Kind     string  `json:"kind"`
				Terminal bool    `json:"terminal"`
				Count    int64   `json:"count"`
				Value    float64 `json:"value"`
				Cards    []struct {
					ID           uint   `json:"id"`
					Name         string `json:"name"`
					CompanyName  string `json:"company_name"`
					FromProspect bool   `json:"from_prospect"`
				} `json:"cards"`
			} `json:"lanes"`
		} `json:"zones"`
	} `json:"data"`
}

func (r overviewResp) lane(zone, name string) (count int64, value float64, cards int, found bool) {
	for _, z := range r.Data.Zones {
		if z.Key != zone {
			continue
		}
		for _, l := range z.Lanes {
			if l.Name == name {
				return l.Count, l.Value, len(l.Cards), true
			}
		}
	}
	return 0, 0, 0, false
}

func getOverview(t *testing.T, app *fiber.App, user *models.User, query string) (overviewResp, *http.Response) {
	t.Helper()
	var out overviewResp
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/pipeline/overview"+query, nil, user.ID, user.Role)
	resp := doJSON(t, app, req, &out)
	return out, resp
}

// TestPipelineOverview_LanesAndTerminalWindow guards the core shape: open
// lanes list every current record regardless of period, terminal lanes (Won
// here) only those that entered inside the period, and a converted Lead is
// left out of the Lead zone since it's already on the board as its Deal.
func TestPipelineOverview_LanesAndTerminalWindow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	old := time.Now().AddDate(0, 0, -60)
	recent := time.Now().AddDate(0, 0, -2)

	prospect := &models.Prospect{Name: "Warm Prospect", Source: "Social Media", Status: models.ProspectStatusNew}
	require.NoError(t, db.Create(prospect).Error)

	fromProspect := &models.Lead{Name: "From Prospect", Source: models.LeadSourceWebsite, Status: models.LeadStatusContacted, ProspectID: &prospect.ID}
	require.NoError(t, db.Create(fromProspect).Error)

	openDeal := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: "Open Deal", Value: 1000,
		Stage: models.DealStageNegotiation, Status: models.DealStatusOpen, Probability: intPtr(50)}
	require.NoError(t, db.Create(openDeal).Error)
	// Created long ago, but still open: must stay on the board.
	require.NoError(t, db.Model(openDeal).UpdateColumns(map[string]interface{}{"created_at": old, "stage_entered_at": old}).Error)

	wonRecent := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: "Won This Week", Value: 5000,
		Stage: models.DealStageWon, Status: models.DealStatusWon, StageEnteredAt: &recent}
	require.NoError(t, db.Create(wonRecent).Error)
	wonOld := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: "Won Long Ago", Value: 9000,
		Stage: models.DealStageWon, Status: models.DealStatusWon, StageEnteredAt: &old}
	require.NoError(t, db.Create(wonOld).Error)

	converted := &models.Lead{Name: "Already A Deal", Source: models.LeadSourceWebsite, Status: models.LeadStatusQualified, ConvertedDealID: &openDeal.ID}
	require.NoError(t, db.Create(converted).Error)

	out, resp := getOverview(t, app, admin, "")
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	count, _, _, ok := out.lane("prospect", "New")
	require.True(t, ok)
	assert.Equal(t, int64(1), count)
	_, _, _, ok = out.lane("prospect", "Converted")
	assert.True(t, ok, "Converted is always appended as a terminal Prospect lane")

	count, _, _, _ = out.lane("lead", "Qualified")
	assert.Equal(t, int64(0), count, "a converted Lead shows as its Deal, not in the Lead zone")

	count, value, cards, _ := out.lane("deal", "Negotiation")
	assert.Equal(t, int64(1), count, "open lanes ignore the period")
	assert.Equal(t, 1000.0, value)
	assert.Equal(t, 1, cards)

	count, value, _, _ = out.lane("deal", "Won")
	assert.Equal(t, int64(1), count, "terminal lanes only show what entered inside the period")
	assert.Equal(t, 5000.0, value)

	assert.Equal(t, int64(1), out.Data.Summary.Won.Current)
	assert.Equal(t, 5000.0, out.Data.Summary.Won.Value)
	assert.Equal(t, int64(1), out.Data.Summary.OpenPipeline.Count)
	assert.Equal(t, 1000.0, out.Data.Summary.OpenPipeline.Value)
	assert.InDelta(t, 500.0, out.Data.Summary.OpenPipeline.WeightedValue, 0.01)
	// Prospect + 2 Leads created now; the open Deal was backdated, the two Won ones weren't.
	assert.Equal(t, int64(1), out.Data.Summary.NewProspects.Current)
	assert.Equal(t, int64(2), out.Data.Summary.NewLeads.Current)
	assert.Equal(t, int64(2), out.Data.Summary.NewDeals.Current)

	var sawLead, sawDeal bool
	for _, z := range out.Data.Zones {
		for _, l := range z.Lanes {
			for _, c := range l.Cards {
				if c.ID == fromProspect.ID && z.Key == "lead" {
					sawLead = true
					assert.True(t, c.FromProspect)
					assert.Equal(t, "From Prospect", c.Name)
				}
				if c.ID == openDeal.ID && z.Key == "deal" {
					sawDeal = true
					assert.Equal(t, company.Name, c.CompanyName)
				}
			}
		}
	}
	assert.True(t, sawLead && sawDeal, "both cards must be returned with their fields populated")
}

// TestPipelineOverview_PeriodAndPreviousWindow guards the date window: an
// explicit date_from/date_to is inclusive, and the comparison window is the
// same length immediately before it.
func TestPipelineOverview_PeriodAndPreviousWindow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	out, resp := getOverview(t, app, admin, "?date_from=2026-09-14&date_to=2026-09-20")
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "2026-09-14", out.Data.Period.DateFrom)
	assert.Equal(t, "2026-09-20", out.Data.Period.DateTo)
	assert.Equal(t, "2026-09-07", out.Data.Period.PrevDateFrom)
	assert.Equal(t, "2026-09-13", out.Data.Period.PrevDateTo)

	_, resp = getOverview(t, app, admin, "?date_from=not-a-date")
	assert.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
	_, resp = getOverview(t, app, admin, "?date_from=2026-09-20&date_to=2026-09-14")
	assert.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
}

// TestPipelineOverview_Filters guards the owner and search filters applying
// to every zone and to the summary alike.
func TestPipelineOverview_Filters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	require.NoError(t, db.Create(&models.Lead{Name: "Rep Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew, AssignedTo: &rep.ID}).Error)
	require.NoError(t, db.Create(&models.Lead{Name: "Other Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew}).Error)

	out, _ := getOverview(t, app, admin, "?assigned_to="+itoa(rep.ID))
	count, _, _, _ := out.lane("lead", "New")
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(1), out.Data.Summary.NewLeads.Current)

	out, _ = getOverview(t, app, admin, "?search=other")
	count, _, _, _ = out.lane("lead", "New")
	assert.Equal(t, int64(1), count)
}

// TestPipelineOverview_StageEnteredAtStamps guards that moving a record to a
// new lane restamps stage_entered_at, and a same-lane save doesn't.
func TestPipelineOverview_StageEnteredAtStamps(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	lead := seedLead(t, db, nil)
	old := time.Now().AddDate(0, 0, -30)
	require.NoError(t, db.Model(lead).UpdateColumn("stage_entered_at", old).Error)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/"+itoa(lead.ID)+"/status",
		map[string]interface{}{"status": string(lead.Status), "position": 5}, rep.ID, rep.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	var reloaded models.Lead
	require.NoError(t, db.First(&reloaded, lead.ID).Error)
	require.NotNil(t, reloaded.StageEnteredAt)
	assert.WithinDuration(t, old, *reloaded.StageEnteredAt, time.Second, "same-lane reorder keeps the timestamp")

	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/"+itoa(lead.ID)+"/status",
		map[string]interface{}{"status": "Contacted"}, rep.ID, rep.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	require.NoError(t, db.First(&reloaded, lead.ID).Error)
	assert.WithinDuration(t, time.Now(), *reloaded.StageEnteredAt, 5*time.Second, "a lane change restamps it")

	deal := seedDeal(t, db, nil)
	require.NotNil(t, deal.StageEnteredAt, "BeforeCreate stamps new rows")
}

// TestPipelineOverview_CardOrderLimitAndPrevious guards the per-lane card
// query (open lanes longest-waiting first, terminal lanes most recent first,
// capped by card_limit while count stays exact) and the summary's
// previous-window counts.
func TestPipelineOverview_CardOrderLimitAndPrevious(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	daysAgo := func(n int) time.Time { return time.Now().AddDate(0, 0, -n) }

	for _, lead := range []struct {
		name   string
		status models.LeadStatus
		ago    int
	}{
		{"Waiting 3d", models.LeadStatusNew, 3},
		{"Waiting 20d", models.LeadStatusNew, 20},
		{"Waiting 9d", models.LeadStatusNew, 9},
		{"Disq 1d", models.LeadStatusDisqualified, 1},
		{"Disq 5d", models.LeadStatusDisqualified, 5},
	} {
		entered := daysAgo(lead.ago)
		row := &models.Lead{Name: lead.name, Source: models.LeadSourceWebsite, Status: lead.status, StageEnteredAt: &entered}
		require.NoError(t, db.Create(row).Error)
		// Created in the previous 7-day window, so they count as "previous".
		require.NoError(t, db.Model(row).UpdateColumn("created_at", daysAgo(10)).Error)
	}

	var out overviewResp
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/pipeline/overview?card_limit=2", nil, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &out).StatusCode)

	names := func(lane string) []string {
		for _, z := range out.Data.Zones {
			for _, l := range z.Lanes {
				if z.Key == "lead" && l.Name == lane {
					var n []string
					for _, c := range l.Cards {
						n = append(n, c.Name)
					}
					return n
				}
			}
		}
		return nil
	}
	count, _, _, _ := out.lane("lead", "New")
	assert.Equal(t, int64(3), count, "count stays exact past card_limit")
	assert.Equal(t, []string{"Waiting 20d", "Waiting 9d"}, names("New"), "open lane: longest-waiting first, capped")
	assert.Equal(t, []string{"Disq 1d", "Disq 5d"}, names("Disqualified"), "terminal lane: most recent first")

	assert.Equal(t, int64(0), out.Data.Summary.NewLeads.Current)
	assert.Equal(t, int64(5), out.Data.Summary.NewLeads.Previous)
}

// TestPipelineOverview_RoleGate: Marketing is allowed (FR-CRM-123), Production isn't.
func TestPipelineOverview_RoleGate(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)
	production := testutil.CreateUser(t, db, models.RoleProduction)

	_, resp := getOverview(t, app, marketing, "")
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	_, resp = getOverview(t, app, production, "")
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

func intPtr(v int) *int { return &v }

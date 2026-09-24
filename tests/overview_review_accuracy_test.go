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

// Overview Pipeline review-accuracy tests (FR-CRM-123, 2026-09-24): exact
// highlight counts, per-stage stale thresholds, move direction, cohort
// conversion, and the stage-config / stage-move API changes behind them.

type reviewOverviewResp struct {
	Data struct {
		Summary struct {
			Conversion struct {
				ProspectToLead struct{ Cohort, Converted int64 } `json:"prospect_to_lead"`
				LeadToDeal     struct{ Cohort, Converted int64 } `json:"lead_to_deal"`
				DealToWon      struct{ Cohort, Converted int64 } `json:"deal_to_won"`
			} `json:"conversion"`
		} `json:"summary"`
		Highlight struct {
			Stale          int64   `json:"stale"`
			Moved          int64   `json:"moved"`
			Slipped        int64   `json:"slipped"`
			StaleDeals     int64   `json:"stale_deals"`
			StaleDealValue float64 `json:"stale_deal_value"`
		} `json:"highlight"`
		Zones []struct {
			Key   string `json:"key"`
			Lanes []struct {
				Name      string `json:"name"`
				StaleDays int    `json:"stale_days"`
				Cards     []struct {
					Name          string `json:"name"`
					PreviousStage string `json:"previous_stage"`
					Direction     string `json:"direction"`
				} `json:"cards"`
			} `json:"lanes"`
		} `json:"zones"`
	} `json:"data"`
}

func getReviewOverview(t *testing.T, app *fiber.App, user *models.User, query string) reviewOverviewResp {
	t.Helper()
	var out reviewOverviewResp
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/pipeline/overview"+query, nil, user.ID, user.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &out).StatusCode)
	return out
}

// TestOverview_HighlightExactBeyondCardLimit: counts cover every open-lane
// record, not only the cards returned (card_limit=1 here).
func TestOverview_HighlightExactBeyondCardLimit(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	old := time.Now().AddDate(0, 0, -30)
	for i := 0; i < 3; i++ {
		d := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: "Old Deal", Value: 100,
			Stage: models.DealStageNegotiation, Status: models.DealStatusOpen, StageEnteredAt: &old}
		require.NoError(t, db.Create(d).Error)
	}

	out := getReviewOverview(t, app, admin, "?card_limit=1")
	assert.Equal(t, int64(3), out.Data.Highlight.Stale)
	assert.Equal(t, int64(3), out.Data.Highlight.StaleDeals)
	assert.Equal(t, 300.0, out.Data.Highlight.StaleDealValue)
}

// TestOverview_PerStageStaleDays: a stage's own stale_days replaces the
// 14-day default, on the lane and in the counts.
func TestOverview_PerStageStaleDays(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	thirty := 30
	require.NoError(t, db.Model(&models.PipelineStage{}).Where("name = ?", "Negotiation").Update("stale_days", thirty).Error)

	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	twentyDaysAgo := time.Now().AddDate(0, 0, -20)
	for _, stage := range []models.DealStage{models.DealStageNegotiation, models.DealStageQualified} {
		d := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: string(stage), Value: 10,
			Stage: stage, Status: models.DealStatusOpen, StageEnteredAt: &twentyDaysAgo}
		require.NoError(t, db.Create(d).Error)
	}

	out := getReviewOverview(t, app, admin, "")
	lanes := map[string]int{}
	for _, z := range out.Data.Zones {
		if z.Key == "deal" {
			for _, l := range z.Lanes {
				lanes[l.Name] = l.StaleDays
			}
		}
	}
	assert.Equal(t, 30, lanes["Negotiation"])
	assert.Equal(t, models.DefaultStaleDays, lanes["Qualified"])
	assert.Equal(t, 0, lanes["Won"], "terminal lanes are never stale")
	assert.Equal(t, int64(1), out.Data.Highlight.StaleDeals, "20 days is stale in Qualified (14) but not Negotiation (30)")
}

// TestOverview_MoveDirectionAndSlipped: moves record the lane they left, the
// card says forward/backward, and Slipped counts backward moves.
func TestOverview_MoveDirectionAndSlipped(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	back := seedDeal(t, db, nil)
	fwd := seedDeal(t, db, nil)
	// Created long ago so the moves below count as moves, not creations.
	past := time.Now().AddDate(0, 0, -10)
	require.NoError(t, db.Model(&models.Deal{}).Where("id IN ?", []uint{back.ID, fwd.ID}).UpdateColumn("created_at", past).Error)

	move := func(id uint, stage string) {
		req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(id)+"/stage", map[string]interface{}{"stage": stage}, admin.ID, admin.Role)
		require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	}
	move(back.ID, "Negotiation")
	move(back.ID, "Qualified") // slipped back
	move(fwd.ID, "Proposal Sent")

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, back.ID).Error)
	require.NotNil(t, reloaded.PreviousStage)
	assert.Equal(t, "Negotiation", *reloaded.PreviousStage)

	out := getReviewOverview(t, app, admin, "")
	dirs := map[string]string{}
	for _, z := range out.Data.Zones {
		if z.Key != "deal" {
			continue
		}
		for _, l := range z.Lanes {
			for _, c := range l.Cards {
				if l.Name == "Qualified" || l.Name == "Proposal Sent" {
					dirs[l.Name] = c.Direction
				}
			}
		}
	}
	assert.Equal(t, "backward", dirs["Qualified"])
	assert.Equal(t, "forward", dirs["Proposal Sent"])
	assert.Equal(t, int64(2), out.Data.Highlight.Moved)
	assert.Equal(t, int64(1), out.Data.Highlight.Slipped)
}

// TestOverview_CohortConversion: of the records created in the period, how
// many reached the next step — never above 100%.
func TestOverview_CohortConversion(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	converted := &models.Prospect{Name: "Became Lead", Source: "Social Media", Status: models.ProspectStatusConverted}
	require.NoError(t, db.Create(converted).Error)
	lead := &models.Lead{Name: "From P", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew, ProspectID: &converted.ID}
	require.NoError(t, db.Create(lead).Error)
	require.NoError(t, db.Model(converted).UpdateColumn("converted_lead_id", lead.ID).Error)
	require.NoError(t, db.Create(&models.Prospect{Name: "Still P", Source: "Social Media", Status: models.ProspectStatusNew}).Error)
	// Lots of Leads created directly — this used to push "conversion" past 100%.
	for i := 0; i < 4; i++ {
		require.NoError(t, db.Create(&models.Lead{Name: "Direct", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew}).Error)
	}
	won := seedDeal(t, db, nil)
	require.NoError(t, db.Model(won).UpdateColumns(map[string]interface{}{"stage": "Won", "status": "won"}).Error)
	seedDeal(t, db, nil)

	c := getReviewOverview(t, app, admin, "").Data.Summary.Conversion
	assert.Equal(t, int64(2), c.ProspectToLead.Cohort)
	assert.Equal(t, int64(1), c.ProspectToLead.Converted)
	assert.Equal(t, int64(5), c.LeadToDeal.Cohort)
	assert.Equal(t, int64(0), c.LeadToDeal.Converted)
	assert.Equal(t, int64(2), c.DealToWon.Cohort)
	assert.Equal(t, int64(1), c.DealToWon.Converted)
}

// TestStageStaleDays_PartialUpdate: stale_days only changes when sent, so an
// older client that omits it can't wipe an Admin's threshold.
func TestStageStaleDays_PartialUpdate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	var stage models.PipelineStage
	require.NoError(t, db.Where("name = ?", "Negotiation").First(&stage).Error)
	path := "/api/v1/admin/pipeline-stages/" + itoa(stage.ID)
	patch := func(body map[string]interface{}) int {
		req := testutil.AuthRequest(t, http.MethodPatch, path, body, admin.ID, admin.Role)
		return doJSON(t, app, req, nil).StatusCode
	}
	reload := func() *int {
		var s models.PipelineStage
		require.NoError(t, db.First(&s, stage.ID).Error)
		return s.StaleDays
	}
	base := map[string]interface{}{"name": stage.Name, "sort_order": stage.SortOrder, "is_won_stage": false, "is_lost_stage": false}

	with := func(v interface{}) map[string]interface{} {
		m := map[string]interface{}{}
		for k, val := range base {
			m[k] = val
		}
		m["stale_days"] = v
		return m
	}
	require.Equal(t, fiber.StatusOK, patch(with(21)))
	require.NotNil(t, reload())
	assert.Equal(t, 21, *reload())

	require.Equal(t, fiber.StatusOK, patch(base))
	require.NotNil(t, reload(), "omitting stale_days keeps it")
	assert.Equal(t, 21, *reload())

	require.Equal(t, fiber.StatusOK, patch(with(nil)))
	assert.Nil(t, reload(), "explicit null resets to the default")

	assert.Equal(t, fiber.StatusUnprocessableEntity, patch(with(0)))
	assert.Equal(t, fiber.StatusUnprocessableEntity, patch(with(400)))
}

// TestDealStageMove_LostReason: the quick-move saves a lost_reason sent with
// a move into Lost, and rejects an invalid one.
func TestDealStageMove_LostReason(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	path := "/api/v1/deals/" + itoa(deal.ID) + "/stage"

	req := testutil.AuthRequest(t, http.MethodPatch, path, map[string]interface{}{"stage": "Lost", "lost_reason": "not-a-reason"}, admin.ID, admin.Role)
	assert.Equal(t, fiber.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode)

	req = testutil.AuthRequest(t, http.MethodPatch, path, map[string]interface{}{"stage": "Lost", "lost_reason": "competitor"}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	require.NotNil(t, reloaded.LostReason)
	assert.Equal(t, models.LostReasonCompetitor, *reloaded.LostReason)
	assert.Equal(t, models.DealStatusLost, reloaded.Status)
}

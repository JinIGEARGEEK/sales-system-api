package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// winDeal marks deal Won through Save (so the BeforeSave hook stamps
// won_at), then backdates the win to wonAt.
func winDeal(t *testing.T, db *gorm.DB, deal *models.Deal, value float64, wonAt time.Time) {
	t.Helper()
	deal.Status, deal.Stage, deal.Value = models.DealStatusWon, models.DealStageWon, value
	require.NoError(t, db.Save(deal).Error)
	setWonAt(t, db, deal.ID, wonAt)
}

type wonMetricsSummary struct {
	Data struct {
		WonValue        float64 `json:"won_value"`
		WinRate         float64 `json:"win_rate"`
		AvgDealSize     float64 `json:"avg_deal_size"`
		DealsCount      int64   `json:"deals_count"`
		TotalDealsCount int64   `json:"total_deals_count"`
		StageBreakdown  []struct {
			Stage string  `json:"stage"`
			Value float64 `json:"value"`
			Count int64   `json:"count"`
		} `json:"stage_breakdown"`
		TeamPerformance []struct {
			UserID   uint    `json:"user_id"`
			WonCount int64   `json:"won_count"`
			WonValue float64 `json:"won_value"`
			WinRate  float64 `json:"win_rate"`
		} `json:"team_performance"`
	} `json:"data"`
}

// TestDashboardSummary_WonMetricsCountByWonAt guards "won this period"
// meaning won in the period (deals.won_at), not created in it: a Deal
// created months ago and won inside the window counts, one created inside
// the window but won before it doesn't. Lost Deals count by when they
// entered the Lost lane, and avg_deal_size averages won Deals only
// (FR-CRM-057) — a large open Deal must not drag it.
func TestDashboardSummary_WonMetricsCountByWonAt(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	inWindow := time.Date(2026, 9, 10, 12, 0, 0, 0, ictZone)
	before := time.Date(2026, 8, 1, 12, 0, 0, 0, ictZone)

	// Created long before, won inside the window: counts.
	oldWon := seedDeal(t, db, &rep.ID)
	winDeal(t, db, oldWon, 1000, inWindow)
	setCreatedAt(t, db, &models.Deal{}, oldWon.ID, before)
	// Created inside the window, won inside it: counts.
	newWon := seedDeal(t, db, &rep.ID)
	winDeal(t, db, newWon, 3000, inWindow)
	setCreatedAt(t, db, &models.Deal{}, newWon.ID, inWindow)
	// Created inside the window but won before it (data fixed up later):
	// not won this period.
	earlyWon := seedDeal(t, db, &rep.ID)
	winDeal(t, db, earlyWon, 50000, before)
	setCreatedAt(t, db, &models.Deal{}, earlyWon.ID, inWindow)
	// Lost inside the window (entered the Lost lane in it), created before.
	lost := seedDeal(t, db, &rep.ID)
	reason := models.LostReasonPrice
	lost.Status, lost.Stage, lost.LostReason = models.DealStatusLost, models.DealStageLost, &reason
	require.NoError(t, db.Save(lost).Error)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", lost.ID).
		UpdateColumns(map[string]interface{}{"stage_entered_at": inWindow, "created_at": before}).Error)
	// A big open Deal created in the window: never part of avg_deal_size.
	open := seedDeal(t, db, &rep.ID)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", open.ID).UpdateColumn("value", 900000).Error)
	setCreatedAt(t, db, &models.Deal{}, open.ID, inWindow)

	var out wonMetricsSummary
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet,
		"/api/v1/dashboard/summary?date_from=2026-09-01&date_to=2026-09-30", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 4000.0, out.Data.WonValue, "won in the window, whenever created")
	assert.InDelta(t, 2000.0, out.Data.AvgDealSize, 0.001, "average of the two Deals won in the window")
	assert.InDelta(t, 100.0*2/3, out.Data.WinRate, 0.01, "2 won ÷ (2 won + 1 lost) in the window")

	stages := map[string]int64{}
	for _, s := range out.Data.StageBreakdown {
		stages[s.Stage] = s.Count
	}
	assert.EqualValues(t, 2, stages["Won"], "the Won bar agrees with won_value")
	assert.EqualValues(t, 1, stages["Lost"])
	assert.EqualValues(t, 1, stages["Lead"], "open Deals still count by created_at")

	require.Len(t, out.Data.TeamPerformance, 1)
	assert.EqualValues(t, 2, out.Data.TeamPerformance[0].WonCount)
	assert.Equal(t, 4000.0, out.Data.TeamPerformance[0].WonValue)

	// Created in the window: newWon, earlyWon, open.
	assert.EqualValues(t, 3, out.Data.DealsCount)
	assert.EqualValues(t, 5, out.Data.TotalDealsCount)
}

// TestDashboardSummary_AvgDealSizeIsWonOnly guards FR-CRM-057 with no date
// window at all: open and lost Deals are excluded from avg_deal_size.
func TestDashboardSummary_AvgDealSizeIsWonOnly(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	winDeal(t, db, seedDeal(t, db, nil), 1000, time.Now())
	winDeal(t, db, seedDeal(t, db, nil), 2000, time.Now())
	open := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", open.ID).UpdateColumn("value", 100000).Error)

	var out wonMetricsSummary
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/summary", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.InDelta(t, 1500.0, out.Data.AvgDealSize, 0.001)
}

func reloadDeal(t *testing.T, db *gorm.DB, id uint) models.Deal {
	t.Helper()
	var deal models.Deal
	require.NoError(t, db.First(&deal, id).Error)
	return deal
}

// TestDealWonAt_MaintainedOnEveryPath guards won_at tracking status through
// the API: set on the move into Won (stage drag, full update, create, lead
// conversion), kept on a re-save of an already-won Deal, cleared on a reopen.
func TestDealWonAt_MaintainedOnEveryPath(t *testing.T) {
	app, db := testutil.App(t)
	setRequireSignedContract(t, db, false)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	deal := seedDeal(t, db, nil)
	assert.Nil(t, reloadDeal(t, db, deal.ID).WonAt, "an open Deal has no won_at")

	// PATCH /deals/:id/stage into Won stamps it.
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage",
		map[string]interface{}{"stage": "Won"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	won := reloadDeal(t, db, deal.ID)
	require.NotNil(t, won.WonAt)

	// A full-record re-save of the already-won Deal keeps the original stamp.
	firstWin := time.Date(2026, 9, 1, 9, 0, 0, 0, ictZone)
	setWonAt(t, db, deal.ID, firstWin)
	body := map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Renamed",
		"value": 1000, "stage": "Won", "status": "won",
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resaved := reloadDeal(t, db, deal.ID)
	require.NotNil(t, resaved.WonAt)
	assert.True(t, resaved.WonAt.Equal(firstWin), "re-saving a won Deal must not move its win")

	// Reopening clears it.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage",
		map[string]interface{}{"stage": "Negotiation"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, reloadDeal(t, db, deal.ID).WonAt, "a reopened Deal is no longer won")

	// PUT back into Won stamps it again.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	rewon := reloadDeal(t, db, deal.ID)
	require.NotNil(t, rewon.WonAt)
	assert.True(t, rewon.WonAt.After(firstWin))

	// Created straight into Won.
	var created struct {
		Data models.Deal `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Born won", "value": 500, "stage": "Won",
	}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotNil(t, created.Data.WonAt)
	assert.NotNil(t, reloadDeal(t, db, created.Data.ID).WonAt)

	// A Lead converted straight into Won.
	company := seedCompany(t, db)
	lead := seedLead(t, db, &company.ID)
	var converted struct {
		Data struct {
			Deal models.Deal `json:"deal"`
		} `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"deal": map[string]interface{}{"title": "Converted won", "value": 700, "stage": "Won"},
	}, admin.ID, admin.Role), &converted)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, models.DealStatusWon, converted.Data.Deal.Status)
	assert.NotNil(t, reloadDeal(t, db, converted.Data.Deal.ID).WonAt)
}

// TestBackfillDealWonAt guards the boot backfill: a won Deal without
// won_at gets its stage_entered_at; a non-won Deal with a stale won_at is
// cleared; a won Deal already stamped is left alone.
func TestBackfillDealWonAt(t *testing.T) {
	_, db := testutil.App(t)

	entered := time.Date(2026, 7, 15, 10, 0, 0, 0, ictZone)
	legacy := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", legacy.ID).
		UpdateColumns(map[string]interface{}{"status": "won", "stage": "Won", "stage_entered_at": entered, "won_at": nil}).Error)

	stale := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", stale.ID).UpdateColumn("won_at", entered).Error)

	stamped := seedDeal(t, db, nil)
	keep := time.Date(2026, 9, 2, 10, 0, 0, 0, ictZone)
	winDeal(t, db, stamped, 1000, keep)

	require.NoError(t, database.BackfillDealWonAt(db))
	require.NoError(t, database.BackfillDealWonAt(db), "re-runnable")

	got := reloadDeal(t, db, legacy.ID).WonAt
	require.NotNil(t, got)
	assert.True(t, got.Equal(entered))
	assert.Nil(t, reloadDeal(t, db, stale.ID).WonAt)
	got = reloadDeal(t, db, stamped.ID).WonAt
	require.NotNil(t, got)
	assert.True(t, got.Equal(keep))
}

// TestWinLossReasons_CountsByCloseDate guards the win/loss report's date
// range reading when a Deal closed, not when it was created.
func TestWinLossReasons_CountsByCloseDate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	inWindow := time.Date(2026, 9, 10, 12, 0, 0, 0, ictZone)
	before := time.Date(2026, 8, 1, 12, 0, 0, 0, ictZone)

	counted := seedDeal(t, db, nil)
	winDeal(t, db, counted, 1000, inWindow)
	setCreatedAt(t, db, &models.Deal{}, counted.ID, before)
	notCounted := seedDeal(t, db, nil)
	winDeal(t, db, notCounted, 1000, before)
	setCreatedAt(t, db, &models.Deal{}, notCounted.ID, inWindow)

	var out struct {
		Data []struct {
			Reason string `json:"reason"`
			Count  int64  `json:"count"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet,
		"/api/v1/reports/win-loss-reasons?date_from=2026-09-01&date_to=2026-09-30", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 1)
	assert.Equal(t, "won", out.Data[0].Reason)
	assert.EqualValues(t, 1, out.Data[0].Count)
}

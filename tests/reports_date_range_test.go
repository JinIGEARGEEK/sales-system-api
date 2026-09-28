package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// dateRangeEdges are four instants around the local day 2026-09-10: the
// first two fall on it, the last two just outside. The first is the day's
// first minutes (still 9 September in UTC); the second its last hour (past
// 00:00 of date_to, so date_to must cover its whole day).
var dateRangeEdges = []time.Time{
	time.Date(2026, 9, 10, 0, 30, 0, 0, ictZone),
	time.Date(2026, 9, 10, 23, 30, 0, 0, ictZone),
	time.Date(2026, 9, 9, 23, 30, 0, 0, ictZone),
	time.Date(2026, 9, 11, 0, 30, 0, 0, ictZone),
}

const dateRangeQueryString = "date_from=2026-09-10&date_to=2026-09-10"

func setCreatedAt(t *testing.T, db *gorm.DB, model interface{}, id uint, at time.Time) {
	t.Helper()
	require.NoError(t, db.Model(model).Where("id = ?", id).UpdateColumn("created_at", at).Error)
}

// TestDateRangeFilters_InclusiveServerLocalDays guards every report,
// dashboard and the audit log reading date_from/date_to as whole
// server-local (Bangkok) days: from local midnight, through the end of
// date_to. Each endpoint sees the same four rows (dateRangeEdges) and must
// count exactly the two on 10 September.
func TestDateRangeFilters_InclusiveServerLocalDays(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	referrer := seedCompany(t, db)
	companyType := models.RelatedTypeCompany

	for _, at := range dateRangeEdges {
		lead := &models.Lead{
			Name: "Edge Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusQualified,
			ReferredByType: &companyType, ReferredByID: &referrer.ID,
		}
		require.NoError(t, db.Create(lead).Error)
		setCreatedAt(t, db, &models.Lead{}, lead.ID, at)

		prospect := seedProspect(t, db, nil)
		setCreatedAt(t, db, &models.Prospect{}, prospect.ID, at)

		deal := seedDeal(t, db, nil)
		require.NoError(t, db.Model(deal).Updates(map[string]interface{}{"status": models.DealStatusWon, "stage": "Won"}).Error)
		setCreatedAt(t, db, &models.Deal{}, deal.ID, at)

		require.NoError(t, db.Create(&models.AuditLogEntry{
			EntityType: "deal", EntityID: deal.ID, Action: "stage_changed", ActorID: admin.ID, CreatedAt: at,
		}).Error)
	}

	get := func(path string, out interface{}) {
		t.Helper()
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, path, nil, admin.ID, admin.Role), out)
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
	}

	var leadSource struct {
		Data []struct {
			Source string `json:"source"`
			Total  int64  `json:"total"`
		} `json:"data"`
	}
	get("/api/v1/reports/lead-source-conversion?"+dateRangeQueryString, &leadSource)
	require.Len(t, leadSource.Data, 1)
	assert.EqualValues(t, 2, leadSource.Data[0].Total, "lead-source-conversion")

	var prospectSource struct {
		Data []struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}
	get("/api/v1/reports/prospect-source-conversion?"+dateRangeQueryString, &prospectSource)
	require.Len(t, prospectSource.Data, 1)
	assert.EqualValues(t, 2, prospectSource.Data[0].Total, "prospect-source-conversion")

	var referrers struct {
		Data []struct {
			LeadsReferred int64 `json:"leads_referred"`
		} `json:"data"`
	}
	get("/api/v1/reports/top-referrers?"+dateRangeQueryString, &referrers)
	require.Len(t, referrers.Data, 1)
	assert.EqualValues(t, 2, referrers.Data[0].LeadsReferred, "top-referrers")

	var winLoss struct {
		Data []struct {
			Reason string `json:"reason"`
			Count  int64  `json:"count"`
		} `json:"data"`
	}
	get("/api/v1/reports/win-loss-reasons?"+dateRangeQueryString, &winLoss)
	require.Len(t, winLoss.Data, 1)
	assert.EqualValues(t, 2, winLoss.Data[0].Count, "win-loss-reasons")

	var leadSummary struct {
		Data struct {
			TotalLeads int64 `json:"total_leads"`
		} `json:"data"`
	}
	get("/api/v1/dashboard/lead-summary?"+dateRangeQueryString, &leadSummary)
	assert.EqualValues(t, 2, leadSummary.Data.TotalLeads, "dashboard lead-summary")

	var prospectSummary struct {
		Data struct {
			TotalProspects int64 `json:"total_prospects"`
		} `json:"data"`
	}
	get("/api/v1/dashboard/prospect-summary?"+dateRangeQueryString, &prospectSummary)
	assert.EqualValues(t, 2, prospectSummary.Data.TotalProspects, "dashboard prospect-summary")

	var summary struct {
		Data struct {
			StageBreakdown []struct {
				Count int64 `json:"count"`
			} `json:"stage_breakdown"`
		} `json:"data"`
	}
	get("/api/v1/dashboard/summary?"+dateRangeQueryString, &summary)
	var deals int64
	for _, s := range summary.Data.StageBreakdown {
		deals += s.Count
	}
	assert.EqualValues(t, 2, deals, "dashboard summary")

	var audit struct {
		Total int64 `json:"total"`
	}
	get("/api/v1/audit-log?entity_type=deal&"+dateRangeQueryString, &audit)
	assert.EqualValues(t, 2, audit.Total, "audit-log")
}

// TestDateRangeFilters_RejectBadRanges guards the 422 for a malformed or
// reversed date range on the reports that used to pass the raw string to
// Postgres (a 500) or silently return nothing.
func TestDateRangeFilters_RejectBadRanges(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	paths := []string{
		"/api/v1/reports/lead-source-conversion",
		"/api/v1/reports/lead-source-conversion/export",
		"/api/v1/reports/prospect-source-conversion",
		"/api/v1/reports/top-referrers",
		"/api/v1/reports/win-loss-reasons",
		"/api/v1/reports/win-loss-reasons/export",
		"/api/v1/reports/sales-cycle",
		"/api/v1/dashboard/summary",
		"/api/v1/dashboard/lead-summary",
		"/api/v1/dashboard/prospect-summary",
		"/api/v1/audit-log",
	}
	for _, path := range paths {
		for _, q := range []string{"?date_from=not-a-date", "?date_to=2026-02-30", "?date_from=2026-09-10&date_to=2026-09-09"} {
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, path+q, nil, admin.ID, admin.Role), nil)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, path+q)
		}
	}
}

// TestSalesCycle_DateRangeNarrowsAuditRows guards fetchSalesCycle filtering
// stage_changed rows to the window's Deals in SQL: a Deal outside the range
// contributes nothing, one inside still does.
func TestSalesCycle_DateRangeNarrowsAuditRows(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	for _, at := range []time.Time{dateRangeEdges[1], dateRangeEdges[3]} {
		deal := seedDeal(t, db, nil)
		setCreatedAt(t, db, &models.Deal{}, deal.ID, at)
		require.NoError(t, db.Create(&models.AuditLogEntry{
			EntityType: "deal", EntityID: deal.ID, Action: "stage_changed", ActorID: admin.ID,
			Before: models.JSONMap{"stage": "Lead"}, After: models.JSONMap{"stage": "Qualified"},
			CreatedAt: at.Add(48 * time.Hour),
		}).Error)
	}

	var out struct {
		Data struct {
			ByStage []struct {
				Key   string `json:"key"`
				Count int    `json:"count"`
			} `json:"by_stage"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/sales-cycle?"+dateRangeQueryString, nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data.ByStage, 1)
	assert.Equal(t, "Lead", out.Data.ByStage[0].Key)
	assert.Equal(t, 1, out.Data.ByStage[0].Count, "only the in-range Deal's transition counts")
}

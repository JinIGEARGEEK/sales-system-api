package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestWinLossReasons_GroupsByReason guards GET /reports/win-loss-reasons:
// won deals collapse into a single "won" bucket, lost deals group by their
// lost_reason code.
func TestWinLossReasons_GroupsByReason(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	won := seedDeal(t, db, nil)
	won.Status = models.DealStatusWon
	won.Value = 1000
	require.NoError(t, db.Save(won).Error)

	lostPrice := seedDeal(t, db, nil)
	reason := models.LostReasonPrice
	lostPrice.Status = models.DealStatusLost
	lostPrice.LostReason = &reason
	lostPrice.Value = 500
	require.NoError(t, db.Save(lostPrice).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/win-loss-reasons", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			Reason string  `json:"reason"`
			Count  int64   `json:"count"`
			Value  float64 `json:"value"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	byReason := map[string]float64{}
	for _, r := range out.Data {
		byReason[r.Reason] = r.Value
	}
	assert.Equal(t, 1000.0, byReason["won"])
	assert.Equal(t, 500.0, byReason["price"])
}

// TestStalledDeals_ExcludesRecentlyActiveDeals guards GET /reports/stalled-deals:
// an open deal with a recent Activity is excluded; one with none since
// creation (backdated) is included with a correct days_stalled.
func TestStalledDeals_ExcludesRecentlyActiveDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	fresh := seedDeal(t, db, nil)
	require.NoError(t, db.Create(&models.Activity{Type: "call", Subject: "Check-in", RelatedType: "deal", RelatedID: fresh.ID}).Error)

	stale := seedDeal(t, db, nil)
	backdated := time.Now().AddDate(0, 0, -20)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", stale.ID).Update("created_at", backdated).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/stalled-deals?min_days=14", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			DealID      uint `json:"deal_id"`
			DaysStalled int  `json:"days_stalled"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ids := map[uint]int{}
	for _, r := range out.Data {
		ids[r.DealID] = r.DaysStalled
	}
	assert.NotContains(t, ids, fresh.ID, "a deal with a recent activity must not be reported as stalled")
	require.Contains(t, ids, stale.ID)
	assert.GreaterOrEqual(t, ids[stale.ID], 19)
}

// TestOutstandingBalance_OnlyPartiallyPaidWonDeals guards GET
// /reports/outstanding-balance: a fully-paid Won deal is excluded, a
// partially-paid one is included with the correct remaining amount.
func TestOutstandingBalance_OnlyPartiallyPaidWonDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	fullyPaid := seedDeal(t, db, nil)
	fullyPaid.Status = models.DealStatusWon
	fullyPaid.Value = 1000
	require.NoError(t, db.Save(fullyPaid).Error)
	require.NoError(t, db.Create(&models.Payment{DealID: fullyPaid.ID, Amount: 1000, PaidAt: time.Now()}).Error)

	partiallyPaid := seedDeal(t, db, nil)
	partiallyPaid.Status = models.DealStatusWon
	partiallyPaid.Value = 1000
	require.NoError(t, db.Save(partiallyPaid).Error)
	require.NoError(t, db.Create(&models.Payment{DealID: partiallyPaid.ID, Amount: 400, PaidAt: time.Now()}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/outstanding-balance", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			DealID            uint    `json:"deal_id"`
			OutstandingAmount float64 `json:"outstanding_amount"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	byID := map[uint]float64{}
	for _, r := range out.Data {
		byID[r.DealID] = r.OutstandingAmount
	}
	assert.NotContains(t, byID, fullyPaid.ID, "a fully-paid deal must not appear in outstanding balance")
	assert.Equal(t, 600.0, byID[partiallyPaid.ID])
}

// TestQuotesExpiringSoon_OnlyWithinWindow guards GET
// /reports/quotes-expiring-soon: a Sent quote expiring in 3 days is
// included at within_days=7; one expiring in 30 days is not.
func TestQuotesExpiringSoon_OnlyWithinWindow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	soon := seedDeal(t, db, nil)
	soonDate := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	require.NoError(t, db.Create(&models.Quote{DealID: soon.ID, Items: models.JSONItems{{Description: "Item", Qty: 1, Price: 100}}, ValidityDate: &soonDate, Status: models.QuoteStatusSent}).Error)

	later := seedDeal(t, db, nil)
	laterDate := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	require.NoError(t, db.Create(&models.Quote{DealID: later.ID, Items: models.JSONItems{}, ValidityDate: &laterDate, Status: models.QuoteStatusSent}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/quotes-expiring-soon?within_days=7", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			DealID     uint    `json:"deal_id"`
			TotalValue float64 `json:"total_value"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ids := map[uint]float64{}
	for _, r := range out.Data {
		ids[r.DealID] = r.TotalValue
	}
	require.Contains(t, ids, soon.ID)
	assert.Equal(t, 100.0, ids[soon.ID])
	assert.NotContains(t, ids, later.ID, "a quote expiring outside the window must not be reported")
}

// TestContractsStuck_ExcludesSignedAndRecent guards GET
// /reports/contracts-stuck: a signed contract is never reported; an
// old, still-unsigned one is.
func TestContractsStuck_ExcludesSignedAndRecent(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	signedDeal := seedDeal(t, db, nil)
	signedAt := time.Now()
	require.NoError(t, db.Create(&models.Contract{DealID: signedDeal.ID, Status: models.ContractStatusSigned, SignedDate: &signedAt}).Error)

	stuckDeal := seedDeal(t, db, nil)
	stuck := &models.Contract{DealID: stuckDeal.ID, Status: models.ContractStatusSent}
	require.NoError(t, db.Create(stuck).Error)
	require.NoError(t, db.Model(&models.Contract{}).Where("id = ?", stuck.ID).Update("updated_at", time.Now().AddDate(0, 0, -20)).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/contracts-stuck?min_days=14", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			DealID uint `json:"deal_id"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ids := make([]uint, 0, len(out.Data))
	for _, r := range out.Data {
		ids = append(ids, r.DealID)
	}
	assert.NotContains(t, ids, signedDeal.ID)
	assert.Contains(t, ids, stuckDeal.ID)
}

// TestProjectsAtRisk_OnlyOverdueAndUnfinished guards GET
// /reports/projects-at-risk: a Completed project past its target date is
// excluded; an In Progress one past its target date is included.
func TestProjectsAtRisk_OnlyOverdueAndUnfinished(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	pastDate := time.Now().AddDate(0, 0, -5)

	doneProject := &models.Project{CompanyID: company.ID, Name: "Finished", Status: models.ProjectStatusCompleted, StartDate: time.Now(), TargetEndDate: &pastDate}
	require.NoError(t, db.Create(doneProject).Error)

	atRiskProject := &models.Project{CompanyID: company.ID, Name: "Late", Status: models.ProjectStatusInProgress, StartDate: time.Now(), TargetEndDate: &pastDate}
	require.NoError(t, db.Create(atRiskProject).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/projects-at-risk", nil, admin.ID, admin.Role)
	var out struct {
		Data []struct {
			ProjectID uint `json:"project_id"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ids := make([]uint, 0, len(out.Data))
	for _, r := range out.Data {
		ids = append(ids, r.ProjectID)
	}
	assert.NotContains(t, ids, doneProject.ID)
	assert.Contains(t, ids, atRiskProject.ID)
}

// TestReports_EmptyResultsAreJSONArraysNotNull guards a real bug caught
// while building these endpoints: a `var rows []T` destination that Scan
// never touches (because zero rows matched) stays a nil Go slice, which
// json.Marshal renders as `null`, not `[]` — and the frontend crashes
// calling .map()/.length on a null response body. Every list-shaped report
// endpoint must return `[]` on an empty result, not `null`.
func TestReports_EmptyResultsAreJSONArraysNotNull(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	paths := []string{
		"/api/v1/reports/lead-source-conversion",
		"/api/v1/reports/customers-by-product-status",
		"/api/v1/reports/win-loss-reasons",
		"/api/v1/reports/stalled-deals",
		"/api/v1/reports/outstanding-balance",
		"/api/v1/reports/quotes-expiring-soon",
		"/api/v1/reports/contracts-stuck",
		"/api/v1/reports/projects-at-risk",
	}
	for _, path := range paths {
		req := testutil.AuthRequest(t, http.MethodGet, path, nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		var out struct {
			Data []interface{} `json:"data"`
		}
		testutil.DecodeJSON(t, resp, &out)
		assert.NotNil(t, out.Data, "%s: response data must be [] on an empty result, not null", path)
	}
}

// TestReports_ForbiddenForSalesRep guards the shared route-level gate: a
// Sales Rep must be forbidden from every new report endpoint, same as the
// two pre-existing ones.
func TestReports_ForbiddenForSalesRep(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	paths := []string{
		"/api/v1/reports/win-loss-reasons",
		"/api/v1/reports/stalled-deals",
		"/api/v1/reports/outstanding-balance",
		"/api/v1/reports/quotes-expiring-soon",
		"/api/v1/reports/contracts-stuck",
		"/api/v1/reports/projects-at-risk",
	}
	for _, path := range paths {
		req := testutil.AuthRequest(t, http.MethodGet, path, nil, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, path)
	}
}

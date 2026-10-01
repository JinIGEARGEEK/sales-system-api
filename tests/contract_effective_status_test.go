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

// TestContracts_SignedPastEndDateReportsExpired guards the read-derived
// effective_status: a Signed contract whose end_date has passed reads
// "expired", while status stays the stored "signed" — so the FR-CRM-045
// Won gate still counts it, and a client saving status back can't turn it
// into a stored "expired" by accident.
func TestContracts_SignedPastEndDateReportsExpired(t *testing.T) {
	app, db := testutil.App(t)
	setRequireSignedContract(t, db, true)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	lapsed := seedContract(t, db, deal.ID, models.ContractStatusSigned)
	yesterday := time.Now().AddDate(0, 0, -1)
	require.NoError(t, db.Model(lapsed).UpdateColumn("end_date", yesterday.Format("2006-01-02")).Error)
	current := seedContract(t, db, deal.ID, models.ContractStatusSigned)
	require.NoError(t, db.Model(current).UpdateColumn("end_date", time.Now().Format("2006-01-02")).Error)
	draft := seedContract(t, db, deal.ID, models.ContractStatusDraft)
	require.NoError(t, db.Model(draft).UpdateColumn("end_date", yesterday.Format("2006-01-02")).Error)

	var list struct {
		Data []struct {
			ID              uint   `json:"id"`
			Status          string `json:"status"`
			EffectiveStatus string `json:"effective_status"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", nil, admin.ID, admin.Role), &list)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	byID := map[uint][2]string{}
	for _, c := range list.Data {
		byID[c.ID] = [2]string{c.Status, c.EffectiveStatus}
	}
	assert.Equal(t, [2]string{"signed", "expired"}, byID[lapsed.ID], "past its last day")
	assert.Equal(t, [2]string{"signed", "signed"}, byID[current.ID], "end_date is the last day in force")
	assert.Equal(t, [2]string{"draft", "draft"}, byID[draft.ID], "never in force, so never expires")

	// The write response carries it too.
	var updated struct {
		Data struct {
			Status          string `json:"status"`
			EffectiveStatus string `json:"effective_status"`
		} `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(lapsed.ID),
		map[string]interface{}{"status": "signed"}, admin.ID, admin.Role), &updated)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "signed", updated.Data.Status)
	assert.Equal(t, "expired", updated.Data.EffectiveStatus)

	// Only the lapsed Signed contract exists for this Deal's gate: still Won-able.
	only := seedDeal(t, db, nil)
	gate := seedContract(t, db, only.ID, models.ContractStatusSigned)
	require.NoError(t, db.Model(gate).UpdateColumn("end_date", yesterday.Format("2006-01-02")).Error)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(only.ID)+"/stage",
		map[string]interface{}{"stage": "Won"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a lapsed Signed contract still satisfies the Won gate")
}

// TestPipelineStages_ExposeDefaultProbability guards default_probability on
// GET /admin/pipeline-stages: the number Deal create/update would default
// to, so the frontend needn't keep its own table.
func TestPipelineStages_ExposeDefaultProbability(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	var out struct {
		Data []struct {
			Name               string `json:"name"`
			IsActive           bool   `json:"is_active"`
			DefaultProbability int    `json:"default_probability"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/pipeline-stages", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	byName := map[string]int{}
	for _, s := range out.Data {
		if s.IsActive {
			byName[s.Name] = s.DefaultProbability
		}
	}
	// The seeded funnel: four open stages spread 10..90, then Won/Lost.
	assert.Equal(t, map[string]int{
		"Lead": 10, "Qualified": 36, "Proposal Sent": 63, "Negotiation": 90, "Won": 100, "Lost": 0,
	}, byName)

	// And it's what a Deal created without a probability actually gets.
	deal := seedDeal(t, db, nil)
	var created struct {
		Data models.Deal `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Defaulted", "stage": "Proposal Sent",
	}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, created.Data.Probability)
	assert.Equal(t, byName["Proposal Sent"], *created.Data.Probability)
}

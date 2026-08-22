package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// FR-CRM-045: "System shall require a Contract be marked 'Signed' before a
// Deal can be marked 'Won' (configurable, not hard-enforced by default)."
// These guard AppSettings.RequireSignedContractBeforeWon's enforcement in
// DealHandler.UpdateStage/Update — the toggle itself (settings_test.go
// already covers PATCH /admin/settings' general field-write behavior).

// setRequireSignedContract sets the toggle directly (bypassing the audit-
// logged PATCH endpoint, which isn't what these tests are exercising) and
// always registers a cleanup restoring it to false afterward — app_settings
// is a singleton row never truncated between tests, so without this any test
// here that enables the toggle would otherwise leak `true` into every test
// that runs later in the same suite binary, including unrelated pre-existing
// tests elsewhere that move a Deal to Won with no Signed Contract in mind.
func setRequireSignedContract(t *testing.T, db *gorm.DB, enabled bool) {
	t.Helper()
	require.NoError(t, db.Model(&models.AppSettings{}).Where("id = 1").
		Update("require_signed_contract_before_won", enabled).Error)
	if enabled {
		t.Cleanup(func() {
			require.NoError(t, db.Model(&models.AppSettings{}).Where("id = 1").
				Update("require_signed_contract_before_won", false).Error)
		})
	}
}

func seedContract(t *testing.T, db *gorm.DB, dealID uint, status models.ContractStatus) *models.Contract {
	t.Helper()
	contract := &models.Contract{DealID: dealID, Status: status}
	require.NoError(t, db.Create(contract).Error)
	return contract
}

// TestUpdateStage_DefaultAllowsWonWithoutSignedContract guards the "not
// hard-enforced by default" half of FR-CRM-045 — with the toggle at its
// default (false), moving a Deal to Won requires no Contract at all, exactly
// like before this feature existed.
func TestUpdateStage_DefaultAllowsWonWithoutSignedContract(t *testing.T) {
	app, db := testutil.App(t)
	// app_settings is a singleton row never truncated between tests — force
	// the "default" state explicitly rather than assuming no earlier test in
	// this suite (e.g. TestSettingsUpdate_RequireSignedContractBeforeWon) has
	// left the toggle enabled.
	setRequireSignedContract(t, db, false)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage", map[string]interface{}{
		"stage": "Won",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, models.DealStatusWon, reloaded.Status)
}

// TestUpdateStage_BlocksWonWithoutSignedContractWhenEnabled guards the actual
// enforcement: once the toggle is on, a Deal with zero Signed Contracts must
// 422 on the transition into Won, and the Deal must not be mutated.
func TestUpdateStage_BlocksWonWithoutSignedContractWhenEnabled(t *testing.T) {
	app, db := testutil.App(t)
	setRequireSignedContract(t, db, true)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	originalStage := deal.Stage

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage", map[string]interface{}{
		"stage": "Won",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, originalStage, reloaded.Stage, "blocked transition must not mutate the deal's stage")
	assert.NotEqual(t, models.DealStatusWon, reloaded.Status)
}

// TestUpdateStage_AllowsWonWithSignedContractWhenEnabled guards the success
// path: a Deal with at least one Signed Contract can move to Won once the
// toggle is on. A Draft contract on the same Deal must not count.
func TestUpdateStage_AllowsWonWithSignedContractWhenEnabled(t *testing.T) {
	app, db := testutil.App(t)
	setRequireSignedContract(t, db, true)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	seedContract(t, db, deal.ID, models.ContractStatusDraft)
	seedContract(t, db, deal.ID, models.ContractStatusSigned)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage", map[string]interface{}{
		"stage": "Won",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, models.DealStatusWon, reloaded.Status)
}

// TestDealUpdate_DoesNotRecheckAlreadyWonDeal guards a real regression caught
// during review of this feature: PUT /deals/:id (the Overview form) always
// resubmits the deal's current stage/status on every save, even for an
// unrelated field edit. If the toggle is turned on after a Deal is already
// Won without a Signed Contract, re-saving that deal must still succeed —
// the check only fires on the actual transition into Won, not on every
// subsequent save of a deal that's already Won.
func TestDealUpdate_DoesNotRecheckAlreadyWonDeal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	deal.Stage = models.DealStageWon
	deal.Status = models.DealStatusWon
	require.NoError(t, db.Save(deal).Error)

	// Enabled *after* the deal is already Won without a Signed Contract.
	setRequireSignedContract(t, db, true)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), map[string]interface{}{
		"company_id": deal.CompanyID,
		"contact_id": deal.ContactID,
		"title":      "Edited title, unrelated to winning the deal",
		"value":      deal.Value,
		"stage":      "Won",
		"status":     "won",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "editing an already-Won deal must not be blocked by a toggle enabled afterward")

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, "Edited title, unrelated to winning the deal", reloaded.Title)
}

// TestDealCreate_BlocksDirectWonCreationWithoutSignedContractWhenEnabled
// guards Create's path: a brand-new Deal can never have a pre-existing
// Contract, so creating one directly in a Won stage must 422 when the
// toggle is enabled, with no special-casing needed (dealID 0 naturally
// finds zero Signed Contracts).
func TestDealCreate_BlocksDirectWonCreationWithoutSignedContractWhenEnabled(t *testing.T) {
	app, db := testutil.App(t)
	setRequireSignedContract(t, db, true)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"company_id": company.ID,
		"contact_id": contact.ID,
		"title":      "Born Won",
		"value":      1000,
		"stage":      "Won",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestContractCreate_RejectsInvalidStatus guards the new enum-membership
// check on Create — previously any string was accepted with no validation
// at all.
func TestContractCreate_RejectsInvalidStatus(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{
		"status": "bogus",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestContractCreate_AllowsEmptyStatus confirms the new check doesn't make
// status required — it still defaults to draft when omitted.
func TestContractCreate_AllowsEmptyStatus(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	var out struct {
		Data models.Contract `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, models.ContractStatusDraft, out.Data.Status)
}

// TestContractUpdate_RejectsInvalidStatus is Update's sibling of the Create
// case above.
func TestContractUpdate_RejectsInvalidStatus(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	contract := seedContract(t, db, deal.ID, models.ContractStatusDraft)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), map[string]interface{}{
		"status": "bogus",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestContractCreate_RejectsQuoteFromAnotherDeal guards the new existence +
// ownership check on quote_id — previously any quote_id (even one belonging
// to a different Deal entirely, or a nonexistent id) was accepted unchecked.
func TestContractCreate_RejectsQuoteFromAnotherDeal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	otherDeal := seedDeal(t, db, nil)
	otherQuote := &models.Quote{DealID: otherDeal.ID}
	require.NoError(t, db.Create(otherQuote).Error)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{
		"quote_id": otherQuote.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestContractCreate_RejectsNonexistentQuote guards the existence half of
// the same check — a quote_id that doesn't exist at all.
func TestContractCreate_RejectsNonexistentQuote(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{
		"quote_id": 999999,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestContractCreate_AllowsQuoteFromSameDeal is the positive-path sibling of
// the two rejection cases above.
func TestContractCreate_AllowsQuoteFromSameDeal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	quote := &models.Quote{DealID: deal.ID}
	require.NoError(t, db.Create(quote).Error)

	var out struct {
		Data models.Contract `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{
		"quote_id": quote.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out.Data.QuoteID)
	assert.Equal(t, quote.ID, *out.Data.QuoteID)
}

// TestContractUpdate_RejectsQuoteFromAnotherDeal is Update's sibling of the
// Create ownership-mismatch case.
func TestContractUpdate_RejectsQuoteFromAnotherDeal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	contract := seedContract(t, db, deal.ID, models.ContractStatusDraft)
	otherDeal := seedDeal(t, db, nil)
	otherQuote := &models.Quote{DealID: otherDeal.ID}
	require.NoError(t, db.Create(otherQuote).Error)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.ID), map[string]interface{}{
		"quote_id": otherQuote.ID,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

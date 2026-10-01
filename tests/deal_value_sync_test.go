package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

type dealDetailResp struct {
	Data struct {
		models.Deal
		ValueQuoteNumber *string `json:"value_quote_number"`
	} `json:"data"`
}

type syncQuoteResp struct {
	Data models.Quote `json:"data"`
}

func storedDeal(t *testing.T, db *gorm.DB, id uint) models.Deal {
	t.Helper()
	var d models.Deal
	require.NoError(t, db.First(&d, id).Error)
	return d
}

// inclTaxQuoteBody is a priced incl_tax quote: 10,700 incl. VAT, so its
// taxable (revenue) amount is 10,000.
func inclTaxQuoteBody(status models.QuoteStatus) map[string]interface{} {
	return map[string]interface{}{
		"status": status, "price_type": "incl_tax", "vat_enabled": true,
		"items": []map[string]interface{}{{"description": "Build", "qty": 1, "price": 10700}},
	}
}

// Creating a quote as accepted syncs the Deal value to its pre-VAT taxable
// amount, links it, audits value_synced, and GET shows value_quote_number.
func TestDealValueSync_CreateAccepted(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	var created syncQuoteResp
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes",
		inclTaxQuoteBody(models.QuoteStatusAccepted), admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	stored := storedDeal(t, db, deal.ID)
	assert.InDelta(t, 10000, stored.Value, 0.001, "taxable amount, not the VAT-inclusive 10,700")
	require.NotNil(t, stored.ValueQuoteID)
	assert.Equal(t, created.Data.ID, *stored.ValueQuoteID)

	entry := lastAudit(t, db, "deal", deal.ID, "value_synced")
	require.NotNil(t, entry)
	assert.EqualValues(t, 1000, entry.Before["value"])
	assert.Nil(t, entry.Before["value_quote_id"])
	assert.EqualValues(t, 10000, entry.After["value"])
	assert.EqualValues(t, created.Data.ID, entry.After["value_quote_id"])
	assert.Equal(t, *created.Data.Number, entry.After["quote_number"])

	var got dealDetailResp
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID), nil, admin.ID, admin.Role), &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, got.Data.ValueQuoteID)
	require.NotNil(t, got.Data.ValueQuoteNumber)
	assert.Equal(t, *created.Data.Number, *got.Data.ValueQuoteNumber)
}

// Accepting via PUT syncs; rejecting it afterwards unlinks, keeping value.
func TestDealValueSync_AcceptThenReject(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	q := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusSent, "QT-SYNC1") // 2 × 500 excl_tax, VAT on

	resp, _ := putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	stored := storedDeal(t, db, deal.ID)
	assert.InDelta(t, 1000, stored.Value, 0.001)
	require.NotNil(t, stored.ValueQuoteID)
	assert.Equal(t, q.ID, *stored.ValueQuoteID)
	require.NotNil(t, lastAudit(t, db, "deal", deal.ID, "value_synced"))

	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "rejected"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	stored = storedDeal(t, db, deal.ID)
	assert.Nil(t, stored.ValueQuoteID)
	assert.InDelta(t, 1000, stored.Value, 0.001, "value stays")
	entry := lastAudit(t, db, "deal", deal.ID, "value_unsynced")
	require.NotNil(t, entry)
	assert.EqualValues(t, q.ID, entry.Before["value_quote_id"])
	assert.Equal(t, "QT-SYNC1", entry.Before["quote_number"])
	assert.Nil(t, entry.After["value_quote_id"])

	var got dealDetailResp
	doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID), nil, admin.ID, admin.Role), &got)
	assert.Nil(t, got.Data.ValueQuoteNumber)
}

// An Accepted quote with no priced items (an uploaded PDF) changes nothing.
func TestDealValueSync_UnpricedAcceptChangesNothing(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	number := "QT-PDF"
	q := &models.Quote{DealID: deal.ID, Status: models.QuoteStatusSent, Number: &number,
		Items: models.JSONItems{}, PriceType: models.QuotePriceTypeExclTax, VatEnabled: true}
	require.NoError(t, db.Create(q).Error)

	resp, _ := putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	stored := storedDeal(t, db, deal.ID)
	assert.InDelta(t, 1000, stored.Value, 0.001)
	assert.Nil(t, stored.ValueQuoteID)
	assert.Nil(t, lastAudit(t, db, "deal", deal.ID, "value_synced"))

	// Rejecting an unsynced quote writes no value_unsynced either.
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "rejected"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, lastAudit(t, db, "deal", deal.ID, "value_unsynced"))
}

// Only drafts can be deleted, and a draft is never the synced quote, so
// deleting one leaves the Deal alone; the FK's ON DELETE SET NULL still
// unlinks a Deal if its quote row is ever removed.
func TestDealValueSync_DeleteDraftAndFK(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	accepted := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusSent, "QT-KEEP")
	resp, _ := putQuote(t, app, admin, accepted.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	draft := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusDraft, "QT-DRAFT")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/quotes/"+itoa(draft.ID), nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	stored := storedDeal(t, db, deal.ID)
	require.NotNil(t, stored.ValueQuoteID)
	assert.Equal(t, accepted.ID, *stored.ValueQuoteID)

	require.NoError(t, db.Exec("DELETE FROM quotes WHERE id = ?", accepted.ID).Error)
	stored = storedDeal(t, db, deal.ID)
	assert.Nil(t, stored.ValueQuoteID)
	assert.InDelta(t, 1000, stored.Value, 0.001)
}

// While synced, PUT /deals/:id can't change value (422 synced_from_quote,
// naming the quote); resending the stored value or editing other fields is
// fine. Once unsynced the value is editable again.
func TestDealValueSync_PutValueGuard(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	q := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusSent, "QT-GUARD")
	q.Items = models.JSONItems{{Description: "Build", Qty: 1, Price: 2500}}
	require.NoError(t, db.Save(q).Error)
	resp, _ := putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	synced := storedDeal(t, db, deal.ID)
	require.InDelta(t, 2500, synced.Value, 0.001)

	body := dealPutBody(&synced, nil)
	body["value"] = 9999
	var errBody sessionErrBody
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role), &errBody)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, []string{"synced_from_quote"}, errBody.Error.Fields["value"])
	assert.Contains(t, errBody.Error.Message, "QT-GUARD")
	assert.InDelta(t, 2500, storedDeal(t, db, deal.ID).Value, 0.001)

	body = dealPutBody(&synced, nil)
	body["title"] = "Renamed"
	var ok dealDetailResp
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role), &ok)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Renamed", ok.Data.Title)
	require.NotNil(t, ok.Data.ValueQuoteID, "a full-row save keeps the link")
	require.NotNil(t, ok.Data.ValueQuoteNumber)
	assert.Equal(t, "QT-GUARD", *ok.Data.ValueQuoteNumber)
	after := storedDeal(t, db, deal.ID)
	require.NotNil(t, after.ValueQuoteID)
	assert.InDelta(t, 2500, after.Value, 0.001)

	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "rejected"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body = dealPutBody(&after, nil)
	body["value"] = 9999
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), body, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.InDelta(t, 9999, storedDeal(t, db, deal.ID).Value, 0.001)
}

// The boot backfill links a Deal whose value already matches its latest
// Accepted quote's taxable amount, and leaves a mismatched one untouched.
func TestBackfillDealValueQuotes(t *testing.T) {
	_, db := testutil.App(t)
	require.NoError(t, db.Where("name = ?", "deal_value_quotes_backfill").Delete(&models.DataMigration{}).Error)

	matched := seedDeal(t, db, nil)                                                   // value 1000
	mq := seedLifecycleQuote(t, db, matched.ID, models.QuoteStatusAccepted, "QT-BF1") // taxable 1000
	mismatched := seedDeal(t, db, nil)
	require.NoError(t, db.Model(mismatched).Update("value", 1234).Error)
	seedLifecycleQuote(t, db, mismatched.ID, models.QuoteStatusAccepted, "QT-BF2")
	unpriced := seedDeal(t, db, nil)
	number := "QT-BF3"
	require.NoError(t, db.Create(&models.Quote{DealID: unpriced.ID, Status: models.QuoteStatusAccepted, Number: &number,
		Items: models.JSONItems{}, PriceType: models.QuotePriceTypeExclTax, VatEnabled: true}).Error)
	deleted := seedDeal(t, db, nil)
	seedLifecycleQuote(t, db, deleted.ID, models.QuoteStatusAccepted, "QT-BF4")
	require.NoError(t, db.Delete(deleted).Error)

	require.NoError(t, database.BackfillDealValueQuotes(db))

	got := storedDeal(t, db, matched.ID)
	require.NotNil(t, got.ValueQuoteID)
	assert.Equal(t, mq.ID, *got.ValueQuoteID)
	assert.InDelta(t, 1000, got.Value, 0.001)

	got = storedDeal(t, db, mismatched.ID)
	assert.Nil(t, got.ValueQuoteID, "mismatch is left unlinked")
	assert.InDelta(t, 1234, got.Value, 0.001, "revenue never rewritten")

	assert.Nil(t, storedDeal(t, db, unpriced.ID).ValueQuoteID)
	var del models.Deal
	require.NoError(t, db.Unscoped().First(&del, deleted.ID).Error)
	assert.Nil(t, del.ValueQuoteID)

	// Runs once.
	require.NoError(t, db.Exec("UPDATE deals SET value_quote_id = NULL WHERE id = ?", matched.ID).Error)
	require.NoError(t, database.BackfillDealValueQuotes(db))
	assert.Nil(t, storedDeal(t, db, matched.ID).ValueQuoteID)
}

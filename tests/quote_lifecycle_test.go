package apitests

import (
	"net/http"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// seedLifecycleQuote stores a priced quote on dealID with the given status
// and number, straight into the DB.
func seedLifecycleQuote(t *testing.T, db *gorm.DB, dealID uint, status models.QuoteStatus, number string) *models.Quote {
	t.Helper()
	q := &models.Quote{
		DealID: dealID, Status: status, Number: &number, ScopeOfWork: "Website",
		Items:     models.JSONItems{{Description: "Build", Qty: 2, Price: 500}},
		PriceType: models.QuotePriceTypeExclTax, VatEnabled: true,
	}
	require.NoError(t, db.Create(q).Error)
	return q
}

func putQuote(t *testing.T, app *fiber.App, admin *models.User, id uint, body map[string]interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	var out map[string]interface{}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(id), body, admin.ID, admin.Role), &out)
	return resp, out
}

func storedQuoteStatus(t *testing.T, db *gorm.DB, id uint) models.QuoteStatus {
	t.Helper()
	var q models.Quote
	require.NoError(t, db.First(&q, id).Error)
	return q.Status
}

// TestQuoteLifecycle_TransitionTable walks every stored-status pair through
// PUT and checks it against the documented table.
func TestQuoteLifecycle_TransitionTable(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	allowed := map[models.QuoteStatus][]models.QuoteStatus{
		models.QuoteStatusDraft:    {models.QuoteStatusSent, models.QuoteStatusAccepted, models.QuoteStatusRejected},
		models.QuoteStatusSent:     {models.QuoteStatusDraft, models.QuoteStatusAccepted, models.QuoteStatusRejected},
		models.QuoteStatusAccepted: {models.QuoteStatusRejected},
		models.QuoteStatusRejected: {},
	}
	n := 0
	for from, tos := range allowed {
		for _, to := range models.ValidQuoteStatuses {
			if to == from {
				continue
			}
			n++
			deal := seedDeal(t, db, nil)
			q := seedLifecycleQuote(t, db, deal.ID, from, "QT-T"+itoa(uint(n)))
			resp, _ := putQuote(t, app, admin, q.ID, map[string]interface{}{"status": to})
			want := http.StatusConflict
			for _, ok := range tos {
				if ok == to {
					want = http.StatusOK
				}
			}
			assert.Equal(t, want, resp.StatusCode, "%s → %s", from, to)
			if want == http.StatusOK {
				assert.Equal(t, to, storedQuoteStatus(t, db, q.ID))
			} else {
				assert.Equal(t, from, storedQuoteStatus(t, db, q.ID))
			}
		}
	}
}

// TestQuoteLifecycle_ExpiredSentCannotBeAccepted: a Sent quote past its
// validity date reads as expired and can't be accepted, but can still be
// rejected or recalled to draft.
func TestQuoteLifecycle_ExpiredSentCannotBeAccepted(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	past := "2020-01-01"
	q := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusSent, "QT-EXP")
	require.NoError(t, db.Model(q).Update("validity_date", past).Error)

	resp, _ := putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "draft", "validity_date": "2099-01-01", "items": q.Items, "scope_of_work": "Website"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "sent", "items": q.Items, "scope_of_work": "Website"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestQuoteLifecycle_AcceptedIsReadOnly: an Accepted quote rejects any
// field change, accepts a resend of its stored values, and still takes
// accepted → rejected — with or without the other fields resent.
func TestQuoteLifecycle_AcceptedIsReadOnly(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	q := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusAccepted, "QT-RO")

	for _, body := range []map[string]interface{}{
		{"items": []map[string]interface{}{{"description": "Build", "qty": 3, "price": 500}}},
		{"scope_of_work": "Changed"},
		{"discount_total": 10},
		{"wht_enabled": true},
		{"notes": "new note"},
		{"validity_date": "2099-12-31"},
		{"status": "rejected", "scope_of_work": "Changed"},
	} {
		resp, out := putQuote(t, app, admin, q.ID, body)
		assert.Equal(t, http.StatusConflict, resp.StatusCode, "%v", body)
		assert.Contains(t, out["error"].(map[string]interface{})["message"], "read-only")
	}

	full := map[string]interface{}{
		"items": q.Items, "scope_of_work": "Website", "price_type": "excl_tax",
		"vat_enabled": true, "wht_enabled": false, "wht_rate": 0, "discount_total": 0, "credit_days": 0,
	}
	resp, _ := putQuote(t, app, admin, q.ID, full)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "resending the stored values is not a change")

	full["status"] = "rejected"
	resp, _ = putQuote(t, app, admin, q.ID, full)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, models.QuoteStatusRejected, storedQuoteStatus(t, db, q.ID))

	// Rejected is terminal and just as read-only.
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"scope_of_work": "Changed"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

// TestQuoteLifecycle_OneAcceptedPerDeal: accepting a second quote names the
// Accepted one; rejecting it first (the UI's replace flow) lets the new one
// through. Create as accepted is held to the same rule.
func TestQuoteLifecycle_OneAcceptedPerDeal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	first := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusAccepted, "QT-FIRST")
	second := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusSent, "QT-SECOND")
	other := seedLifecycleQuote(t, db, seedDeal(t, db, nil).ID, models.QuoteStatusAccepted, "QT-OTHERDEAL")
	_ = other

	resp, out := putQuote(t, app, admin, second.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, out["error"].(map[string]interface{})["message"], "QT-FIRST")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
		"status": "accepted", "items": []map[string]interface{}{{"description": "x", "qty": 1, "price": 1}},
	}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, _ = putQuote(t, app, admin, first.ID, map[string]interface{}{"status": "rejected"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, second.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var count int64
	db.Model(&models.Quote{}).Where("deal_id = ? AND status = ?", deal.ID, models.QuoteStatusAccepted).Count(&count)
	assert.EqualValues(t, 1, count)
}

// TestQuoteLifecycle_ConcurrentAcceptsOnlyOneWins fires several accepts on
// different quotes of one Deal at once: the Deal row lock lets exactly one
// through.
func TestQuoteLifecycle_ConcurrentAcceptsOnlyOneWins(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	const n = 5
	ids := make([]uint, n)
	for i := range ids {
		ids[i] = seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusSent, "QT-RACE"+itoa(uint(i))).ID
	}

	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(ids[i]), map[string]interface{}{"status": "accepted"}, admin.ID, admin.Role)
			resp, err := app.Test(req, -1)
			if err == nil {
				codes[i] = resp.StatusCode
			}
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, code := range codes {
		if code == http.StatusOK {
			ok++
		} else {
			assert.Equal(t, http.StatusConflict, code)
		}
	}
	assert.Equal(t, 1, ok)
	var count int64
	db.Model(&models.Quote{}).Where("deal_id = ? AND status = ?", deal.ID, models.QuoteStatusAccepted).Count(&count)
	assert.EqualValues(t, 1, count)
}

// TestQuoteLifecycle_DeleteOnlyDraft.
func TestQuoteLifecycle_DeleteOnlyDraft(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	for i, status := range []models.QuoteStatus{models.QuoteStatusSent, models.QuoteStatusAccepted, models.QuoteStatusRejected} {
		q := seedLifecycleQuote(t, db, deal.ID, status, "QT-DEL"+itoa(uint(i)))
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/quotes/"+itoa(q.ID), nil, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusConflict, resp.StatusCode, status)
		assert.NoError(t, db.First(&models.Quote{}, q.ID).Error, "%s quote must survive", status)
	}
	draft := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusDraft, "QT-DELDRAFT")
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/quotes/"+itoa(draft.ID), nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// TestQuoteLifecycle_DuplicateRevisionChain: every copy points at the
// chain's root, numbered max + 1, and the original keeps its status.
func TestQuoteLifecycle_DuplicateRevisionChain(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	root := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusAccepted, "QT-ROOT")

	dup := func(id uint) models.Quote {
		var out struct {
			Data models.Quote `json:"data"`
		}
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/quotes/"+itoa(id)+"/duplicate", nil, admin.ID, admin.Role), &out)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		return out.Data
	}
	r1 := dup(root.ID)
	r2 := dup(r1.ID) // copy of a copy still points at the root
	r3 := dup(root.ID)
	for i, r := range []models.Quote{r1, r2, r3} {
		require.NotNil(t, r.RevisionOfID)
		assert.Equal(t, root.ID, *r.RevisionOfID)
		assert.Equal(t, i+1, r.RevisionNo)
		assert.Equal(t, models.QuoteStatusDraft, r.Status)
	}
	assert.Equal(t, models.QuoteStatusAccepted, storedQuoteStatus(t, db, root.ID), "the original is not auto-rejected")

	var reloaded models.Quote
	require.NoError(t, db.First(&reloaded, root.ID).Error)
	assert.Nil(t, reloaded.RevisionOfID)
	assert.Equal(t, 0, reloaded.RevisionNo)

	// The FK: deleting a draft root leaves its copies unlinked.
	draftRoot := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusDraft, "QT-DRAFTROOT")
	c1 := dup(draftRoot.ID)
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/quotes/"+itoa(draftRoot.ID), nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	var copyRow models.Quote
	require.NoError(t, db.First(&copyRow, c1.ID).Error)
	assert.Nil(t, copyRow.RevisionOfID)
}

// TestQuoteLifecycle_FieldValidation covers the 422s with error.fields on
// Create and Update.
func TestQuoteLifecycle_FieldValidation(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	draft := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusDraft, "QT-VAL")
	item := func(qty, price, pct float64) []map[string]interface{} {
		return []map[string]interface{}{{"description": "x", "qty": qty, "price": price, "discount_percent": pct}}
	}

	cases := []struct {
		name  string
		body  map[string]interface{}
		field string
	}{
		{"zero qty", map[string]interface{}{"items": item(0, 10, 0)}, "items[0].qty"},
		{"negative qty", map[string]interface{}{"items": item(-1, 10, 0)}, "items[0].qty"},
		{"negative price", map[string]interface{}{"items": item(1, -5, 0)}, "items[0].price"},
		{"discount_percent over 100", map[string]interface{}{"items": item(1, 10, 101)}, "items[0].discount_percent"},
		{"discount_percent negative", map[string]interface{}{"items": item(1, 10, -1)}, "items[0].discount_percent"},
		{"discount_total over subtotal", map[string]interface{}{"items": item(2, 100, 50), "discount_total": 100.01}, "discount_total"},
		{"wht_rate over 100", map[string]interface{}{"items": item(1, 10, 0), "wht_rate": 101}, "wht_rate"},
		{"bad issue_date", map[string]interface{}{"items": item(1, 10, 0), "issue_date": "31/12/2026"}, "issue_date"},
		{"bad validity_date", map[string]interface{}{"items": item(1, 10, 0), "validity_date": "soon"}, "validity_date"},
	}
	for _, tc := range cases {
		for _, req := range []*http.Request{
			testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", tc.body, admin.ID, admin.Role),
			testutil.AuthRequest(t, http.MethodPut, "/api/v1/quotes/"+itoa(draft.ID), tc.body, admin.ID, admin.Role),
		} {
			var out struct {
				Error struct {
					Fields map[string][]string `json:"fields"`
				} `json:"error"`
			}
			resp := doJSON(t, app, req, &out)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "%s %s", req.Method, tc.name)
			assert.Contains(t, out.Error.Fields, tc.field, "%s %s", req.Method, tc.name)
		}
	}

	// Boundaries are fine: discount_total equal to the subtotal, 100%
	// item discount, 0 price, RFC 3339 and bare dates.
	resp, _ := putQuote(t, app, admin, draft.ID, map[string]interface{}{
		"items":          []map[string]interface{}{{"description": "x", "qty": 2, "price": 100, "discount_percent": 50}, {"description": "free", "qty": 1, "price": 0, "discount_percent": 100}},
		"discount_total": 100, "wht_rate": 3, "issue_date": "2026-10-01", "validity_date": "2026-10-31T17:00:00.000Z",
	})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestQuoteLifecycle_StatusChangesAreAudited.
func TestQuoteLifecycle_StatusChangesAreAudited(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	q := seedLifecycleQuote(t, db, deal.ID, models.QuoteStatusDraft, "QT-AUD")

	resp, _ := putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "sent", "items": q.Items, "scope_of_work": "Website"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"scope_of_work": "Website v2", "items": q.Items})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = putQuote(t, app, admin, q.ID, map[string]interface{}{"status": "accepted"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ?", "quote", q.ID).Order("id").Find(&entries).Error)
	require.Len(t, entries, 2, "only status changes are audited")
	assert.Equal(t, "status_changed", entries[0].Action)
	assert.Equal(t, "draft", entries[0].Before["status"])
	assert.Equal(t, "sent", entries[0].After["status"])
	assert.Equal(t, "sent", entries[1].Before["status"])
	assert.Equal(t, "accepted", entries[1].After["status"])
	assert.Equal(t, admin.ID, entries[1].ActorID)
}

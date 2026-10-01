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

func seedQuote(t *testing.T, db *gorm.DB, dealID uint, number, ref string, createdAt time.Time) *models.Quote {
	t.Helper()
	q := &models.Quote{DealID: dealID, Status: models.QuoteStatusDraft, VatEnabled: true}
	if number != "" {
		q.Number = &number
	}
	if ref != "" {
		q.ReferenceNumber = &ref
	}
	q.CreatedAt = createdAt
	require.NoError(t, db.Create(q).Error)
	return q
}

type quoteSearchPage struct {
	Data []struct {
		ID              uint               `json:"id"`
		DealID          uint               `json:"deal_id"`
		Number          *string            `json:"number"`
		ReferenceNumber *string            `json:"reference_number"`
		Status          models.QuoteStatus `json:"status"`
		DealTitle       string             `json:"deal_title"`
	} `json:"data"`
	Page      int   `json:"page"`
	PerPage   int   `json:"per_page"`
	Total     int64 `json:"total"`
	TotalPage int   `json:"total_page"`
	Next      *int  `json:"next"`
	Prev      *int  `json:"prev"`
}

// TestQuoteSearch covers GET /quotes: number / reference_number / Deal-title
// matching, newest-first order, soft-deleted Deals excluded, the paginated
// envelope, and role access (row scope = GET /deals, Production 403).
func TestQuoteSearch(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	otherRep := testutil.CreateUser(t, db, models.RoleSalesRep)
	production := testutil.CreateUser(t, db, models.RoleProduction)

	repDeal := seedDeal(t, db, &rep.ID)
	otherDeal := seedDeal(t, db, &otherRep.ID)
	require.NoError(t, db.Model(otherDeal).Update("title", "Website Revamp").Error)
	trashedDeal := seedDeal(t, db, nil)

	base := time.Now().Add(-time.Hour)
	q1 := seedQuote(t, db, repDeal.ID, "QT2026090001", "PO-ALPHA", base)
	q2 := seedQuote(t, db, otherDeal.ID, "QT2026090002", "po-beta", base.Add(time.Minute))
	q3 := seedQuote(t, db, repDeal.ID, "QT2026090003", "", base.Add(2*time.Minute))
	seedQuote(t, db, trashedDeal.ID, "QT2026090004", "PO-ALPHA-TRASH", base.Add(3*time.Minute))
	require.NoError(t, db.Delete(trashedDeal).Error)

	get := func(path string, user *models.User) (quoteSearchPage, int) {
		var out quoteSearchPage
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, path, nil, user.ID, user.Role), &out)
		return out, resp.StatusCode
	}

	t.Run("no search lists all live quotes newest first", func(t *testing.T) {
		out, code := get("/api/v1/quotes", admin)
		require.Equal(t, http.StatusOK, code)
		ids := []uint{}
		for _, r := range out.Data {
			ids = append(ids, r.ID)
		}
		assert.Equal(t, []uint{q3.ID, q2.ID, q1.ID}, ids, "trashed deal's quote excluded")
		assert.EqualValues(t, 3, out.Total)
	})

	t.Run("by number", func(t *testing.T) {
		out, code := get("/api/v1/quotes?search=qt2026090002", admin)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Data, 1)
		assert.Equal(t, q2.ID, out.Data[0].ID)
		assert.Equal(t, "Website Revamp", out.Data[0].DealTitle)
		assert.Equal(t, otherDeal.ID, out.Data[0].DealID)
		assert.Equal(t, models.QuoteStatusDraft, out.Data[0].Status)
	})

	t.Run("by reference_number, case-insensitive", func(t *testing.T) {
		out, code := get("/api/v1/quotes?search=po-alpha", admin)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Data, 1, "the trashed deal's PO-ALPHA-TRASH is excluded")
		assert.Equal(t, q1.ID, out.Data[0].ID)
		assert.Equal(t, "Test Deal", out.Data[0].DealTitle)
	})

	t.Run("by deal title", func(t *testing.T) {
		out, code := get("/api/v1/quotes?search=revamp", admin)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Data, 1)
		assert.Equal(t, q2.ID, out.Data[0].ID)
	})

	t.Run("no match", func(t *testing.T) {
		out, code := get("/api/v1/quotes?search=nothing-like-this", admin)
		require.Equal(t, http.StatusOK, code)
		assert.Empty(t, out.Data)
		assert.NotNil(t, out.Data, "empty array, not null")
		assert.EqualValues(t, 0, out.Total)
		assert.Equal(t, 1, out.TotalPage)
	})

	t.Run("LIKE wildcards are literal", func(t *testing.T) {
		out, code := get("/api/v1/quotes?search=%25", admin)
		require.Equal(t, http.StatusOK, code)
		assert.Empty(t, out.Data)
	})

	t.Run("pagination envelope", func(t *testing.T) {
		out, code := get("/api/v1/quotes?per_page=2&page=1", admin)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Data, 2)
		assert.Equal(t, q3.ID, out.Data[0].ID)
		assert.Equal(t, 1, out.Page)
		assert.Equal(t, 2, out.PerPage)
		assert.EqualValues(t, 3, out.Total)
		assert.Equal(t, 2, out.TotalPage)
		require.NotNil(t, out.Next)
		assert.Equal(t, 2, *out.Next)
		assert.Nil(t, out.Prev)

		out, code = get("/api/v1/quotes?per_page=2&page=2", admin)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Data, 1)
		assert.Equal(t, q1.ID, out.Data[0].ID)
		assert.Nil(t, out.Next)
		require.NotNil(t, out.Prev)
		assert.Equal(t, 1, *out.Prev)
	})

	t.Run("Sales Rep sees a colleague's deal's quote, like GET /deals", func(t *testing.T) {
		dealIDs := listIDs(t, app, "/api/v1/deals?search=revamp", rep.ID, rep.Role)
		require.Equal(t, []uint{otherDeal.ID}, dealIDs, "precondition: GET /deals is not row-scoped")
		out, code := get("/api/v1/quotes?search=QT2026090002", rep)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Data, 1)
		assert.Equal(t, q2.ID, out.Data[0].ID)
	})

	t.Run("Marketing allowed", func(t *testing.T) {
		mkt := testutil.CreateUser(t, db, models.RoleMarketing)
		_, code := get("/api/v1/quotes?search=QT", mkt)
		assert.Equal(t, http.StatusOK, code)
	})

	t.Run("Production 403", func(t *testing.T) {
		_, code := get("/api/v1/quotes?search=QT", production)
		assert.Equal(t, http.StatusForbidden, code)
	})
}

// TestQuoteGet covers GET /quotes/:id — the full-page Quote editor's fetch,
// which the frontend called but the API never registered (404 for every id).
func TestQuoteGet(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	otherRep := testutil.CreateUser(t, db, models.RoleSalesRep)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	production := testutil.CreateUser(t, db, models.RoleProduction)

	deal := seedDeal(t, db, &otherRep.ID)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	quote := seedQuote(t, db, deal.ID, "QT2026090010", "PO-9", time.Now())
	require.NoError(t, db.Model(quote).Updates(map[string]interface{}{"status": models.QuoteStatusSent, "validity_date": yesterday}).Error)

	type one struct {
		Data struct {
			ID              uint               `json:"id"`
			Number          string             `json:"number"`
			ReferenceNumber string             `json:"reference_number"`
			Status          models.QuoteStatus `json:"status"`
			DealTitle       string             `json:"deal_title"`
		} `json:"data"`
	}

	var out one
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/quotes/"+itoa(quote.ID), nil, rep.ID, rep.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode, "read-only: no CanWrite check, a colleague's deal is fine")
	assert.Equal(t, quote.ID, out.Data.ID)
	assert.Equal(t, "QT2026090010", out.Data.Number)
	assert.Equal(t, "PO-9", out.Data.ReferenceNumber)
	assert.Equal(t, models.QuoteStatusExpired, out.Data.Status, "effective status")
	assert.Equal(t, "Test Deal", out.Data.DealTitle)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/quotes/999999", nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/quotes/abc", nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "non-numeric id")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/quotes/"+itoa(quote.ID), nil, production.ID, production.Role), nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	require.NoError(t, db.Delete(deal).Error)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/quotes/"+itoa(quote.ID), nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "soft-deleted deal")
}

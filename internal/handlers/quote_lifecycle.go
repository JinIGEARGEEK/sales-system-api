package handlers

import (
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// lifecycleConflict is returned from inside a quote/contract transaction
// to roll it back and answer 409 with message.
type lifecycleConflict struct{ message string }

func (e *lifecycleConflict) Error() string { return e.message }

// respondLifecycleErr turns a quote/contract save error into its response:
// 409 for a lifecycleConflict, 404 when the row vanished mid-request, else
// 500 with internalMsg.
func respondLifecycleErr(c *fiber.Ctx, err error, notFoundMsg, internalMsg string) error {
	var conflict *lifecycleConflict
	switch {
	case errors.As(err, &conflict):
		return utils.Conflict(c, conflict.message)
	case errors.Is(err, gorm.ErrRecordNotFound):
		return utils.NotFound(c, notFoundMsg)
	default:
		return utils.Internal(c, internalMsg)
	}
}

// lockRow takes a FOR UPDATE lock on model's row id inside tx, reading
// only cols into model.
func lockRow(tx *gorm.DB, model interface{}, id uint, cols ...string) error {
	return tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).Select(cols).First(model, id).Error
}

// quoteLabel names a quote in a message: its number, else its id.
func quoteLabel(q models.Quote) string {
	if q.Number != nil && *q.Number != "" {
		return *q.Number
	}
	return fmt.Sprintf("#%d", q.ID)
}

// ensureSoleAcceptedQuote fails with a lifecycleConflict when another quote
// of dealID (other than quoteID; 0 on Create) is already Accepted. Callers
// hold the Deal row lock (lockRow), so two concurrent accepts on one Deal
// run one after the other and the second sees the first.
func ensureSoleAcceptedQuote(tx *gorm.DB, dealID, quoteID uint) error {
	var other models.Quote
	err := tx.Select("id", "number").
		Where("deal_id = ? AND status = ? AND id <> ?", dealID, models.QuoteStatusAccepted, quoteID).
		Order("id").First(&other).Error
	if err == nil {
		return &lifecycleConflict{fmt.Sprintf("quote %s is already accepted on this deal; reject it before accepting another", quoteLabel(other))}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// saveQuote persists an Update. Every save locks the quote row and checks
// its stored status is still oldStatus (what the lifecycle guard checked),
// so a concurrent status change can't be overwritten by a stale full-row
// Save. A status change also locks the Deal row first (always Deal, then
// quote), enforces one Accepted quote per Deal, syncs or unsyncs the Deal's
// value (syncDealValueForQuote), and writes a quote status_changed audit
// entry in the same transaction.
func (h *QuoteHandler) saveQuote(c *fiber.Ctx, quote *models.Quote, oldStatus models.QuoteStatus) error {
	statusChanged := quote.Status != oldStatus
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if statusChanged {
			if err := lockRow(tx, &models.Deal{}, quote.DealID, "id"); err != nil {
				return err
			}
		}
		var stored models.Quote
		if err := lockRow(tx, &stored, quote.ID, "id", "status"); err != nil {
			return err
		}
		if stored.Status != oldStatus {
			return &lifecycleConflict{fmt.Sprintf("this quote was changed to %s meanwhile; reload it and try again", stored.Status)}
		}
		if statusChanged && quote.Status == models.QuoteStatusAccepted {
			if err := ensureSoleAcceptedQuote(tx, quote.DealID, quote.ID); err != nil {
				return err
			}
		}
		if err := tx.Save(quote).Error; err != nil {
			return err
		}
		if !statusChanged {
			return nil
		}
		actorID := middleware.CurrentUserID(c)
		if err := syncDealValueForQuote(tx, quote, oldStatus, actorID); err != nil {
			return err
		}
		return utils.WriteAuditLog(tx, "quote", quote.ID, "status_changed",
			models.JSONMap{"status": oldStatus}, models.JSONMap{"status": quote.Status}, actorID)
	})
	if err != nil {
		return respondLifecycleErr(c, err, "Quote not found", "Failed to update quote")
	}
	return utils.OK(c, withEffectiveStatus(*quote))
}

// checkQuoteTransition is the status half of Update's lifecycle guard: a
// move not in models.CanTransitionQuoteStatus' table is a 409. Writes the
// response itself and returns false on failure.
func checkQuoteTransition(c *fiber.Ctx, quote models.Quote, to models.QuoteStatus) bool {
	if models.CanTransitionQuoteStatus(quote.Status, quote.EffectiveStatus(), to) {
		return true
	}
	msg := fmt.Sprintf("a %s quote can't be changed to %s", quote.Status, to)
	if quote.EffectiveStatus() == models.QuoteStatusExpired && to == models.QuoteStatusAccepted {
		msg = "this quote has expired and can't be accepted; move it back to draft with a new validity_date, or duplicate it"
	}
	_ = utils.Conflict(c, msg)
	return false
}

// lockedQuoteChange returns the first field of form that would change a
// locked (Accepted/Rejected) quote, or "". Only keys present in the body
// count: a status-only PUT ({"status":"rejected"}) changes nothing else,
// and a full-replace PUT that resends the stored values is not a change.
// A null/empty string equals an unset one.
func lockedQuoteChange(c *fiber.Ctx, q models.Quote, form quoteForm) string {
	body, _ := bodyKeys(c)
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	checks := []struct {
		key     string
		changed bool
	}{
		{"items", form.Items != nil && !sameQuoteItems(form.Items, q.Items)},
		{"scope_of_work", form.ScopeOfWork != q.ScopeOfWork},
		{"validity_date", form.ValidityDate != nil && str(form.ValidityDate) != str(q.ValidityDate)},
		{"reference_number", str(form.ReferenceNumber) != str(q.ReferenceNumber)},
		{"issue_date", str(form.IssueDate) != str(q.IssueDate)},
		{"credit_days", form.CreditDays != nil && *form.CreditDays != q.CreditDays},
		{"price_type", form.PriceType != "" && form.PriceType != q.PriceType},
		{"vat_enabled", form.VatEnabled != nil && *form.VatEnabled != q.VatEnabled},
		{"wht_enabled", form.WhtEnabled != nil && *form.WhtEnabled != q.WhtEnabled},
		{"wht_rate", form.WhtRate != nil && *form.WhtRate != q.WhtRate},
		{"discount_total", form.DiscountTotal != nil && *form.DiscountTotal != q.DiscountTotal},
		{"notes", str(form.Notes) != str(q.Notes)},
		{"internal_notes", str(form.InternalNotes) != str(q.InternalNotes)},
	}
	for _, ch := range checks {
		if ch.changed && body.has(ch.key) {
			return ch.key
		}
	}
	return ""
}

// sameQuoteItems compares submitted items with stored ones as sent — not
// re-snapshotted from the Product catalog, whose prices may have moved
// since the quote was saved.
func sameQuoteItems(a, b []models.QuoteItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Description != y.Description || x.Qty != y.Qty || x.Price != y.Price || x.DiscountPercent != y.DiscountPercent {
			return false
		}
		if (x.ProductID == nil) != (y.ProductID == nil) || (x.ProductID != nil && *x.ProductID != *y.ProductID) {
			return false
		}
	}
	return true
}

// quoteFieldErrors collects the per-item and date checks shared by Create
// and Update: qty > 0, price >= 0, discount_percent 0–100 on every item,
// and issue_date/validity_date (when set) parsing as a calendar day. Keys
// are "items[i].qty" etc. Empty means valid.
func quoteFieldErrors(form quoteForm) map[string][]string {
	fields := map[string][]string{}
	for i, item := range form.Items {
		key := fmt.Sprintf("items[%d].", i)
		if item.Qty <= 0 {
			fields[key+"qty"] = []string{"must be > 0"}
		}
		if item.Price < 0 {
			fields[key+"price"] = []string{"must be >= 0"}
		}
		if item.DiscountPercent < 0 || item.DiscountPercent > 100 {
			fields[key+"discount_percent"] = []string{"must be between 0 and 100"}
		}
	}
	for key, v := range map[string]*string{"issue_date": form.IssueDate, "validity_date": form.ValidityDate} {
		if v == nil || *v == "" {
			continue
		}
		if _, ok := calendar.ParseLocalDay(*v); !ok {
			fields[key] = []string{"must be a date (YYYY-MM-DD or RFC 3339)"}
		}
	}
	return fields
}

// quoteItemsSubtotal is the items' summed line totals after each item's
// own discount_percent — what discount_total is taken from. Kept here
// rather than read from utils.ComputeQuoteTotals: the cap is on the
// entered prices whichever price_type they're in.
func quoteItemsSubtotal(items []models.QuoteItem) float64 {
	var subtotal float64
	for _, item := range items {
		subtotal += item.Qty * item.Price * (1 - item.DiscountPercent/100)
	}
	return subtotal
}

// validateQuoteDiscount checks discount_total doesn't exceed the subtotal
// of the items the quote will be saved with (so it can't push the taxable
// amount below zero). Writes the 422 itself and returns false on failure.
func validateQuoteDiscount(c *fiber.Ctx, items []models.QuoteItem, discountTotal float64) bool {
	subtotal := quoteItemsSubtotal(items)
	if discountTotal <= subtotal+utils.MoneyEpsilon {
		return true
	}
	_ = utils.ValidationError(c, "discount_total must not exceed the subtotal", map[string][]string{
		"discount_total": {fmt.Sprintf("must be between 0 and the subtotal (%.2f)", subtotal)},
	})
	return false
}

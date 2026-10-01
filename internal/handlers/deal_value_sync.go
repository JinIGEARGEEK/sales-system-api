package handlers

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// Deal value follows its Accepted quote (api-system-spec.md §7.1/§7.4).
// When a quote with priced items becomes Accepted, the Deal's value is set
// to that quote's pre-VAT taxable amount (what counts as revenue; see
// utils.QuoteTotals.TaxableAmount) and deals.value_quote_id points at it.
// While it does, PUT /deals/:id can't change value. When the quote leaves
// Accepted the link is cleared and the value stays as it is.

// Audit actions on entity_type "deal" for the sync.
const (
	auditDealValueSynced   = "value_synced"
	auditDealValueUnsynced = "value_unsynced"
)

// errCodeSyncedFromQuote is the fields.value code PUT /deals/:id answers
// while the Deal's value is synced from an Accepted quote.
const errCodeSyncedFromQuote = "synced_from_quote"

// syncDealValueForQuote runs inside a quote Create/Update transaction, after
// the quote row is written, when its status moved from oldStatus ("" on
// Create) to quote.Status:
//   - into Accepted with priced items: deal.value = rounded taxable amount,
//     deal.value_quote_id = quote.id, audit value_synced;
//   - out of Accepted while the Deal is synced from this quote: clear
//     value_quote_id (value kept), audit value_unsynced.
//
// Anything else changes nothing. Takes the Deal row lock (callers already
// hold it on every status change, so this just re-reads under it).
func syncDealValueForQuote(tx *gorm.DB, quote *models.Quote, oldStatus models.QuoteStatus, actorID uint) error {
	entering := quote.Status == models.QuoteStatusAccepted && oldStatus != models.QuoteStatusAccepted
	leaving := oldStatus == models.QuoteStatusAccepted && quote.Status != models.QuoteStatusAccepted
	if !entering && !leaving {
		return nil
	}
	var deal models.Deal
	if err := lockRow(tx, &deal, quote.DealID, "id", "value", "value_quote_id"); err != nil {
		return err
	}
	before := models.JSONMap{"value": deal.Value, "value_quote_id": deal.ValueQuoteID}
	if entering {
		value, ok := utils.DealValueFromQuote(quote)
		if !ok {
			return nil
		}
		if err := tx.Exec(`UPDATE deals SET value = ?, value_quote_id = ?, updated_at = NOW() WHERE id = ?`,
			value, quote.ID, deal.ID).Error; err != nil {
			return err
		}
		after := models.JSONMap{"value": value, "value_quote_id": quote.ID, "quote_number": quoteLabel(*quote)}
		return utils.WriteAuditLog(tx, "deal", deal.ID, auditDealValueSynced, before, after, actorID)
	}
	if deal.ValueQuoteID == nil || *deal.ValueQuoteID != quote.ID {
		return nil
	}
	if err := tx.Exec(`UPDATE deals SET value_quote_id = NULL, updated_at = NOW() WHERE id = ?`, deal.ID).Error; err != nil {
		return err
	}
	before["quote_number"] = quoteLabel(*quote)
	after := models.JSONMap{"value": deal.Value, "value_quote_id": nil}
	return utils.WriteAuditLog(tx, "deal", deal.ID, auditDealValueUnsynced, before, after, actorID)
}

// dealValueSyncedErr is returned from inside PUT /deals/:id's transaction
// when the submitted value differs from a value synced from a quote.
type dealValueSyncedErr struct{ quoteNumber string }

func (e *dealValueSyncedErr) Error() string {
	return fmt.Sprintf("value is synced from accepted quote %s; reject that quote to edit it", e.quoteNumber)
}

// respondDealValueSynced writes the 422 for a dealValueSyncedErr.
func respondDealValueSynced(c *fiber.Ctx, e *dealValueSyncedErr) error {
	return utils.ValidationError(c, e.Error(), map[string][]string{"value": {errCodeSyncedFromQuote}})
}

// checkSyncedDealValue returns a dealValueSyncedErr when deal is synced from
// a quote and value isn't its stored value. db may be a transaction.
func checkSyncedDealValue(db *gorm.DB, deal *models.Deal, value float64) error {
	if deal.ValueQuoteID == nil || utils.SameMoney(value, deal.Value) {
		return nil
	}
	return &dealValueSyncedErr{quoteNumber: valueQuoteNumber(db, deal.ValueQuoteID)}
}

// valueQuoteNumber is the label (number, else #id) of quote id, "" for nil.
func valueQuoteNumber(db *gorm.DB, id *uint) string {
	if id == nil {
		return ""
	}
	if label, ok := quoteLabelByID(db, *id); ok {
		return label
	}
	return fmt.Sprintf("#%d", *id)
}

// quoteLabelByID loads quote id's label (quoteLabel); ok false when it
// can't be read.
func quoteLabelByID(db *gorm.DB, id uint) (string, bool) {
	var q models.Quote
	if err := db.Select("id", "number").First(&q, id).Error; err != nil {
		return "", false
	}
	return quoteLabel(q), true
}

// dealDetail is a single-Deal response: the Deal plus the read-only
// value_quote_number (the synced quote's number, null when not synced).
// Lists return plain Deals.
type dealDetail struct {
	models.Deal
	ValueQuoteNumber *string `json:"value_quote_number"`
}

// withValueQuoteNumber builds deal's dealDetail.
func withValueQuoteNumber(db *gorm.DB, deal models.Deal) dealDetail {
	out := dealDetail{Deal: deal}
	if deal.ValueQuoteID != nil {
		if label, ok := quoteLabelByID(db, *deal.ValueQuoteID); ok {
			out.ValueQuoteNumber = &label
		}
	}
	return out
}

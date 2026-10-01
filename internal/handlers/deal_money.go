package handlers

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// Error codes on the 409 a Won Deal with money attached answers to a delete
// or a move out of Won. The frontend tells them apart: WON_DEAL_PROTECTED
// means the caller can't do it at all (not a manager), REASON_REQUIRED means
// a manager can, by retrying with ?reason=.
const (
	errCodeWonDealProtected = "WON_DEAL_PROTECTED"
	errCodeReasonRequired   = "REASON_REQUIRED"
)

// maxOverrideReasonLength caps the ?reason= a manager gives to delete or
// un-win a protected Deal; it's stored in the audit log.
const maxOverrideReasonLength = 500

// skipReasonWonDealWithMoney is the per-id reason bulk archive reports for a
// Won Deal it left alone.
const skipReasonWonDealWithMoney = "won_deal_with_money"

// dealHasMoney reports whether money is attached to the Deal: a Payment that
// isn't deleted, any PaymentInstallment, or a Contract stored as signed.
func dealHasMoney(db *gorm.DB, dealID uint) (bool, error) {
	var has bool
	err := db.Raw(`SELECT
		EXISTS (SELECT 1 FROM payments WHERE deal_id = ? AND deleted_at IS NULL)
		OR EXISTS (SELECT 1 FROM payment_installments WHERE deal_id = ?)
		OR EXISTS (SELECT 1 FROM contracts WHERE deal_id = ? AND status = ?)`,
		dealID, dealID, dealID, models.ContractStatusSigned).Scan(&has).Error
	return has, err
}

// isProtectedWonDeal reports whether deal is Won with money attached — the
// Deals that can't be deleted, archived or moved out of Won casually.
func isProtectedWonDeal(db *gorm.DB, deal *models.Deal) (bool, error) {
	if deal.Status != models.DealStatusWon {
		return false, nil
	}
	return dealHasMoney(db, deal.ID)
}

// guardProtectedWonDeal applies the rule for deleting a Deal or moving it
// out of Won (verb names the action in the message). A Deal that isn't a
// protected Won Deal passes with forced false. A protected one is a 409 for
// anyone but a manager (middleware.IsManager), and a 409 for a manager who
// sends no ?reason=; a manager with a reason passes with forced true and the
// trimmed reason, for the audit log. Returns utils.ErrHandled once a
// response has been written.
func guardProtectedWonDeal(c *fiber.Ctx, db *gorm.DB, deal *models.Deal, verb string) (reason string, forced bool, err error) {
	protected, err := isProtectedWonDeal(db, deal)
	if err != nil {
		_ = utils.Internal(c, "Failed to check the deal's payments")
		return "", false, utils.ErrHandled
	}
	if !protected {
		return "", false, nil
	}
	if !middleware.IsManager(c) {
		_ = utils.ErrorResponse(c, fiber.StatusConflict, errCodeWonDealProtected,
			"This deal is Won and has payments, installments or a signed contract; only a manager can "+verb+" it")
		return "", false, utils.ErrHandled
	}
	reason = strings.TrimSpace(c.Query("reason"))
	if reason == "" {
		_ = utils.ErrorResponse(c, fiber.StatusConflict, errCodeReasonRequired,
			"This deal is Won and has payments, installments or a signed contract; pass ?reason= to "+verb+" it")
		return "", false, utils.ErrHandled
	}
	if len([]rune(reason)) > maxOverrideReasonLength {
		_ = utils.ValidationError(c, "reason is too long", map[string][]string{"reason": {"max 500 characters"}})
		return "", false, utils.ErrHandled
	}
	return reason, true, nil
}

// guardLeavingWon is guardProtectedWonDeal for a move that may take deal
// out of Won: staysWon true (or a Deal that isn't Won) passes untouched.
func guardLeavingWon(c *fiber.Ctx, db *gorm.DB, deal *models.Deal, staysWon bool) (reason string, forced bool, err error) {
	if deal.Status != models.DealStatusWon || staysWon {
		return "", false, nil
	}
	return guardProtectedWonDeal(c, db, deal, "move out of Won")
}

// logDealStageActivity records a stage move as company Activity (it counts
// as customer contact), next to the move's stage_changed audit entry.
func logDealStageActivity(tx *gorm.DB, deal *models.Deal, oldStage models.DealStage, actorID uint) error {
	subject := fmt.Sprintf("Deal stage changed: %s → %s", oldStage, deal.Stage)
	return utils.LogCompanyActivity(tx, deal.CompanyID, subject, actorID)
}

// writeWonReversedAudit records a manager moving a protected Won Deal out of
// Won, with the reason they gave. Written alongside (not instead of) the
// move's own stage_changed entry.
func writeWonReversedAudit(tx *gorm.DB, deal *models.Deal, before models.JSONMap, reason string, actorID uint) error {
	after := models.JSONMap{"stage": deal.Stage, "status": deal.Status, "reason": reason}
	return utils.WriteAuditLog(tx, "deal", deal.ID, "won_reversed", before, after, actorID)
}

// dealReceivable is what the customer owes on the Deal, by the Outstanding
// Balance report's rule (utils.DealReceivable): its latest Accepted Quote's
// taxable amount + VAT when priced, else the Deal value.
func dealReceivable(db *gorm.DB, deal *models.Deal) (float64, error) {
	var quotes []models.Quote
	if err := db.Where("deal_id = ? AND status = ?", deal.ID, models.QuoteStatusAccepted).
		Order("created_at DESC, id DESC").Limit(1).Find(&quotes).Error; err != nil {
		return 0, err
	}
	var latest *models.Quote
	if len(quotes) > 0 {
		latest = &quotes[0]
	}
	amount, _ := utils.DealReceivable(deal.Value, latest)
	return amount, nil
}

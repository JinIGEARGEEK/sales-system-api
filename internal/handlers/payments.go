package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type PaymentHandler struct {
	DB *gorm.DB
}

func NewPaymentHandler(db *gorm.DB) *PaymentHandler {
	return &PaymentHandler{DB: db}
}

// List godoc
// @Summary List payments for a deal (Admin/Sales Rep/Sales Manager)
// @Description Returns a Deal's recorded Payments (newest paid_at first; deleted ones excluded) plus totals: total_paid = Σ amount (cash received, unchanged meaning), total_wht = Σ wht_amount, total_settled = total_paid + total_wht (what the receivable is reduced by). Backs the Deal detail page's Payments tab. api-system-spec.md §7.5.
// @Tags payments
// @Security BearerAuth
// @Produce json
// @Param dealId path int true "Deal ID"
// @Success 200 {object} map[string]interface{} "{ payments: []models.Payment, total_paid: number, total_wht: number, total_settled: number }"
// @Router /deals/{dealId}/payments [get]
func (h *PaymentHandler) List(c *fiber.Ctx) error {
	dealID := c.Params("dealId")
	var payments []models.Payment
	if err := h.DB.Where("deal_id = ?", dealID).Order("paid_at DESC").Find(&payments).Error; err != nil {
		return utils.Internal(c, "Failed to list payments")
	}

	var totalPaid, totalWht float64
	for _, p := range payments {
		totalPaid += p.Amount
		totalWht += p.WhtAmount
	}

	return utils.OK(c, fiber.Map{
		"payments": payments, "total_paid": totalPaid,
		"total_wht": totalWht, "total_settled": totalPaid + totalWht,
	})
}

// paymentForm is shared by Create and Update. Update merges: a field left
// out of the body keeps its stored value (pointers, plus the raw
// installment_id below so an explicit null can unlink).
type paymentForm struct {
	Amount                 *float64             `json:"amount"`
	PaidAt                 *time.Time           `json:"paid_at"`
	Method                 models.PaymentMethod `json:"method"`
	Note                   *string              `json:"note"`
	WhtAmount              *float64             `json:"wht_amount"`
	WhtCertificateReceived *bool                `json:"wht_certificate_received"`
	DocumentNumber         *string              `json:"document_number"`
	// InstallmentID is raw so Update can tell "absent" (keep) from null
	// (unlink) from a number (link) — a plain *uint can't.
	InstallmentID json.RawMessage `json:"installment_id" swaggertype:"integer"`
	// AllowOverpayment lets this save take the Deal's settled total (cash +
	// WHT) past its receivable; without it that's a 422. Not stored — the
	// audit entry records that it was used.
	AllowOverpayment bool `json:"allow_overpayment"`
}

// applyPaymentForm validates form and writes it onto payment (a new row on
// Create, the stored row on Update). Writes the 422 itself and returns false
// on failure, same convention as validateQuoteForm.
func applyPaymentForm(c *fiber.Ctx, db *gorm.DB, payment *models.Payment, form paymentForm) bool {
	if form.Amount != nil {
		if *form.Amount <= 0 {
			_ = utils.ValidationError(c, "amount is required", map[string][]string{"amount": {"required"}})
			return false
		}
		payment.Amount = *form.Amount
	}
	if !models.IsValidPaymentMethod(form.Method) {
		_ = utils.ValidationError(c, "method is invalid", map[string][]string{"method": {"invalid"}})
		return false
	}
	if form.Method != "" {
		payment.Method = form.Method
	}
	if form.PaidAt != nil {
		// Money can't have arrived after today (server-local); any time
		// today is fine.
		if calendar.LocalDaysBetween(time.Now(), *form.PaidAt) > 0 {
			_ = utils.ValidationError(c, "paid_at must not be in the future", map[string][]string{"paid_at": {"must not be in the future"}})
			return false
		}
		payment.PaidAt = *form.PaidAt
	}
	if form.Note != nil {
		payment.Note = *form.Note
	}
	if form.WhtAmount != nil {
		if *form.WhtAmount < 0 {
			_ = utils.ValidationError(c, "wht_amount must be non-negative", map[string][]string{"wht_amount": {"must be >= 0"}})
			return false
		}
		payment.WhtAmount = *form.WhtAmount
	}
	if form.WhtCertificateReceived != nil {
		payment.WhtCertificateReceived = *form.WhtCertificateReceived
	}
	if form.DocumentNumber != nil {
		doc := strings.TrimSpace(*form.DocumentNumber)
		if len(doc) > 64 {
			_ = utils.ValidationError(c, "document_number is too long", map[string][]string{"document_number": {"max 64 characters"}})
			return false
		}
		if doc == "" {
			payment.DocumentNumber = nil
		} else {
			payment.DocumentNumber = &doc
		}
	}
	if len(form.InstallmentID) > 0 {
		if string(form.InstallmentID) == "null" {
			payment.InstallmentID = nil
		} else {
			var id uint
			if err := json.Unmarshal(form.InstallmentID, &id); err != nil || id == 0 {
				_ = utils.ValidationError(c, "installment_id is invalid", map[string][]string{"installment_id": {"invalid"}})
				return false
			}
			var installment models.PaymentInstallment
			if err := db.First(&installment, id).Error; err != nil {
				_ = utils.ValidationError(c, "installment_id not found", map[string][]string{"installment_id": {"not_found"}})
				return false
			}
			if installment.DealID != payment.DealID {
				_ = utils.ValidationError(c, "installment does not belong to this deal", map[string][]string{"installment_id": {"invalid"}})
				return false
			}
			payment.InstallmentID = &id
		}
	}
	return true
}

// paymentSnapshot is a Payment's audit-log before/after: plain values, so
// two snapshots compare with reflect.DeepEqual.
func paymentSnapshot(p models.Payment) models.JSONMap {
	m := models.JSONMap{
		"deal_id": p.DealID, "amount": p.Amount, "wht_amount": p.WhtAmount,
		"paid_at": p.PaidAt.Format(time.RFC3339Nano), "method": p.Method, "note": p.Note,
		"document_number": nil, "installment_id": nil,
		"wht_certificate_received": p.WhtCertificateReceived,
	}
	if p.DocumentNumber != nil {
		m["document_number"] = *p.DocumentNumber
	}
	if p.InstallmentID != nil {
		m["installment_id"] = *p.InstallmentID
	}
	return m
}

// checkPaymentMoneyRules runs the checks that read other Payments, inside
// tx after the Deal row is locked: a non-empty document_number must be
// unique among non-deleted Payments (409; only checked when docChanged, so
// an old duplicate doesn't block editing anything else), and the Deal's
// settled total (cash + WHT) mustn't pass its receivable
// (dealReceivableAmount) by more than utils.MoneyEpsilon unless allowOver
// (422) — checked only when this save raises payment's own settled amount
// above prevSettled. Returns overpaid (the allowance was used) and
// utils.ErrHandled once a response has been written.
func checkPaymentMoneyRules(c *fiber.Ctx, tx *gorm.DB, deal *models.Deal, payment *models.Payment,
	docChanged bool, prevSettled float64, allowOver bool) (overpaid bool, err error) {
	if docChanged && payment.DocumentNumber != nil {
		// Serializes saves of the same number across Deals.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "payment_document_number:"+*payment.DocumentNumber).Error; err != nil {
			return false, err
		}
		var n int64
		if err := tx.Model(&models.Payment{}).
			Where("document_number = ? AND id <> ?", *payment.DocumentNumber, payment.ID).
			Count(&n).Error; err != nil {
			return false, err
		}
		if n > 0 {
			_ = utils.Conflict(c, "Another payment already has this document_number")
			return false, utils.ErrHandled
		}
	}

	if payment.SettledAmount() <= prevSettled+utils.MoneyEpsilon {
		return false, nil
	}
	var others float64
	if err := tx.Model(&models.Payment{}).Where("deal_id = ? AND id <> ?", deal.ID, payment.ID).
		Select("COALESCE(SUM(amount + wht_amount), 0)").Scan(&others).Error; err != nil {
		return false, err
	}
	receivable, err := dealReceivableAmount(tx, deal)
	if err != nil {
		return false, err
	}
	total := others + payment.SettledAmount()
	if total <= receivable+utils.MoneyEpsilon {
		return false, nil
	}
	if !allowOver {
		_ = utils.ValidationError(c,
			fmt.Sprintf("payments would total %.2f (cash + WHT), more than the deal's receivable of %.2f; send allow_overpayment: true to record it anyway", total, receivable),
			map[string][]string{"amount": {"exceeds_receivable"}})
		return false, utils.ErrHandled
	}
	return true, nil
}

// lockDeal re-reads the Deal FOR UPDATE inside tx, so concurrent payment
// saves on one Deal run their total check one at a time.
func lockDeal(tx *gorm.DB, id uint) (*models.Deal, error) {
	var deal models.Deal
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&deal, id).Error; err != nil {
		return nil, err
	}
	return &deal, nil
}

// Create godoc
// @Summary Record a payment (Admin/Sales Rep/Sales Manager)
// @Description Records money received on a Deal. amount (cash received, net of WHT) must be > 0; method must be a valid PaymentMethod; paid_at defaults to now. Optional: wht_amount (>= 0, default 0 — withholding tax the customer deducted; counts as settled), wht_certificate_received (default false — the 50 ทวิ certificate), document_number (FlowAccount receipt/tax-invoice number, max 64 chars, blank = null), installment_id (a PaymentInstallment of the same Deal; that installment is settled first). paid_at may not be after today (server-local, 422). A Lost Deal takes no payments (422). A non-empty document_number already on another non-deleted Payment is 409. If the Deal's cash + WHT would then exceed its receivable (latest Accepted Quote's taxable amount + VAT when it has priced items, else the Deal value) by more than 0.005 it's a 422 on amount ("exceeds_receivable") unless allow_overpayment: true. Writes a payment created audit entry. Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create. api-system-spec.md §7.5.
// @Tags payments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body paymentForm true "Payment fields"
// @Success 201 {object} models.Payment
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Failure 409 {object} map[string]interface{} "document_number already used by another payment"
// @Failure 422 {object} map[string]interface{} "amount/method/paid_at/wht_amount/document_number/installment_id invalid, deal is Lost, or the payment exceeds the receivable"
// @Router /deals/{dealId}/payments [post]
func (h *PaymentHandler) Create(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form paymentForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Amount == nil {
		return utils.ValidationError(c, "amount is required", map[string][]string{"amount": {"required"}})
	}

	if deal.Status == models.DealStatusLost {
		return utils.ValidationError(c, "cannot record a payment on a Lost deal", map[string][]string{"deal_id": {"deal is lost"}})
	}

	actorID := middleware.CurrentUserID(c)
	payment := models.Payment{DealID: deal.ID, PaidAt: time.Now()}
	payment.CreatedBy, payment.UpdatedBy = &actorID, &actorID
	if !applyPaymentForm(c, h.DB, &payment, form) {
		return nil
	}
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := lockDeal(tx, deal.ID)
		if err != nil {
			return err
		}
		overpaid, err := checkPaymentMoneyRules(c, tx, locked, &payment, true, 0, form.AllowOverpayment)
		if err != nil {
			return err
		}
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		after := paymentSnapshot(payment)
		if overpaid {
			after["overpayment_allowed"] = true
		}
		return utils.WriteAuditLog(tx, "payment", payment.ID, "created", nil, after, actorID)
	})
	if errors.Is(err, utils.ErrHandled) {
		return nil
	}
	if err != nil {
		return utils.Internal(c, "Failed to create payment")
	}
	return utils.Created(c, payment)
}

// Update godoc
// @Summary Edit a payment (Admin/Sales Rep/Sales Manager)
// @Description Partial merge: only fields present in the body change (same validation as Create); installment_id: null unlinks. Typical use is ticking wht_certificate_received once the 50 ทวิ arrives, or adding the FlowAccount document_number later. deal_id is immutable. A changed document_number must be unique among non-deleted payments (409); raising amount/wht_amount past the receivable is a 422 unless allow_overpayment: true. Writes a payment updated audit entry (before/after) when something changed. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may edit.
// @Tags payments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Payment ID"
// @Param body body paymentForm true "Fields to change"
// @Success 200 {object} models.Payment
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Payment not found, or deal not found"
// @Failure 409 {object} map[string]interface{} "document_number already used by another payment"
// @Failure 422 {object} map[string]interface{} "Validation error, or the payment exceeds the receivable"
// @Router /payments/{id} [put]
func (h *PaymentHandler) Update(c *fiber.Ctx) error {
	var payment models.Payment
	if err := utils.FindByID(c, h.DB, &payment, "Payment not found"); err != nil {
		return nil
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(payment.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form paymentForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	before := paymentSnapshot(payment)
	prevSettled := payment.SettledAmount()
	if !applyPaymentForm(c, h.DB, &payment, form) {
		return nil
	}
	after := paymentSnapshot(payment)
	if reflect.DeepEqual(before, after) {
		return utils.OK(c, payment)
	}
	docChanged := !reflect.DeepEqual(before["document_number"], after["document_number"])
	actorID := middleware.CurrentUserID(c)
	payment.UpdatedBy = &actorID

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := lockDeal(tx, payment.DealID)
		if err != nil {
			return err
		}
		overpaid, err := checkPaymentMoneyRules(c, tx, locked, &payment, docChanged, prevSettled, form.AllowOverpayment)
		if err != nil {
			return err
		}
		if err := tx.Save(&payment).Error; err != nil {
			return err
		}
		if overpaid {
			after["overpayment_allowed"] = true
		}
		return utils.WriteAuditLog(tx, "payment", payment.ID, "updated", before, after, actorID)
	})
	if errors.Is(err, utils.ErrHandled) {
		return nil
	}
	if err != nil {
		return utils.Internal(c, "Failed to update payment")
	}
	return utils.OK(c, payment)
}

// Delete godoc
// @Summary Delete a payment
// @Description Soft delete of a Payment (deleted_at/deleted_by set; it drops out of every total, list and report) with a payment deleted audit entry holding the row as it was. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may delete.
// @Tags payments
// @Security BearerAuth
// @Param id path int true "Payment ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Payment not found, or deal not found"
// @Router /payments/{id} [delete]
func (h *PaymentHandler) Delete(c *fiber.Ctx) error {
	var payment models.Payment
	if err := utils.FindByID(c, h.DB, &payment, "Payment not found"); err != nil {
		return nil
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(payment.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}
	actorID := middleware.CurrentUserID(c)
	before := paymentSnapshot(payment)
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := utils.GenericSoftDelete(tx, &payment, actorID); err != nil {
			return err
		}
		return utils.WriteAuditLog(tx, "payment", payment.ID, "deleted", before, nil, actorID)
	})
	if err != nil {
		return utils.Internal(c, "Failed to delete payment")
	}
	return utils.NoContent(c)
}

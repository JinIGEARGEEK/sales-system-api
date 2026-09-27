package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

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
// @Description Returns a Deal's recorded Payments (newest paid_at first) plus totals: total_paid = Σ amount (cash received, unchanged meaning), total_wht = Σ wht_amount, total_settled = total_paid + total_wht (what the receivable is reduced by). Backs the Deal detail page's Payments tab. api-system-spec.md §7.5.
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

// Create godoc
// @Summary Record a payment (Admin/Sales Rep/Sales Manager)
// @Description Records money received on a Deal. amount (cash received, net of WHT) must be > 0; method must be a valid PaymentMethod; paid_at defaults to now. Optional: wht_amount (>= 0, default 0 — withholding tax the customer deducted; counts as settled), wht_certificate_received (default false — the 50 ทวิ certificate), document_number (FlowAccount receipt/tax-invoice number, max 64 chars, blank = null), installment_id (a PaymentInstallment of the same Deal; that installment is settled first). Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create. api-system-spec.md §7.5.
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
// @Failure 422 {object} map[string]interface{} "amount/method/wht_amount/document_number/installment_id invalid"
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

	payment := models.Payment{DealID: deal.ID, PaidAt: time.Now()}
	if !applyPaymentForm(c, h.DB, &payment, form) {
		return nil
	}
	if err := h.DB.Create(&payment).Error; err != nil {
		return utils.Internal(c, "Failed to create payment")
	}
	return utils.Created(c, payment)
}

// Update godoc
// @Summary Edit a payment (Admin/Sales Rep/Sales Manager)
// @Description Partial merge: only fields present in the body change (same validation as Create); installment_id: null unlinks. Typical use is ticking wht_certificate_received once the 50 ทวิ arrives, or adding the FlowAccount document_number later. deal_id is immutable. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may edit.
// @Tags payments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Payment ID"
// @Param body body paymentForm true "Fields to change"
// @Success 200 {object} models.Payment
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Payment not found, or deal not found"
// @Failure 422 {object} map[string]interface{} "Validation error"
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
	if !applyPaymentForm(c, h.DB, &payment, form) {
		return nil
	}
	if err := h.DB.Save(&payment).Error; err != nil {
		return utils.Internal(c, "Failed to update payment")
	}
	return utils.OK(c, payment)
}

// Delete godoc
// @Summary Delete a payment
// @Description Hard delete of a Payment. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may delete.
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
	if err := h.DB.Delete(&payment).Error; err != nil {
		return utils.Internal(c, "Failed to delete payment")
	}
	return utils.NoContent(c)
}

package handlers

import (
	"fmt"
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
// @Description Returns installments for a Deal, plus a computed total_paid. Backs the Deal detail page's Payments tab. api-system-spec.md §7.5.
// @Tags payments
// @Security BearerAuth
// @Produce json
// @Param dealId path int true "Deal ID"
// @Success 200 {object} map[string]interface{} "{ payments: []models.Payment, total_paid: number }"
// @Router /deals/{dealId}/payments [get]
func (h *PaymentHandler) List(c *fiber.Ctx) error {
	dealID := c.Params("dealId")
	var payments []models.Payment
	if err := h.DB.Where("deal_id = ?", dealID).Order("paid_at DESC").Find(&payments).Error; err != nil {
		return utils.Internal(c, "Failed to list payments")
	}

	var totalPaid float64
	for _, p := range payments {
		totalPaid += p.Amount
	}

	return utils.OK(c, fiber.Map{"payments": payments, "total_paid": totalPaid})
}

type paymentForm struct {
	Amount float64              `json:"amount"`
	PaidAt *time.Time           `json:"paid_at"`
	Method models.PaymentMethod `json:"method"`
	Note   string               `json:"note"`
}

// Create godoc
// @Summary Record a payment (Admin/Sales Rep/Sales Manager)
// @Description Creates a Payment installment on a Deal. amount must be > 0; method must be a valid PaymentMethod; paid_at defaults to now if omitted. Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create. api-system-spec.md §7.5.
// @Tags payments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body paymentForm true "Payment fields"
// @Success 201 {object} models.Payment
// @Failure 400 {object} map[string]interface{} "Invalid request body, amount is required, or method is invalid"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
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
	if form.Amount <= 0 {
		return utils.ValidationError(c, "amount is required", map[string][]string{"amount": {"required"}})
	}
	if !models.IsValidPaymentMethod(form.Method) {
		return utils.ValidationError(c, "method is invalid", map[string][]string{"method": {"invalid"}})
	}

	paidAt := time.Now()
	if form.PaidAt != nil {
		paidAt = *form.PaidAt
	}

	payment := models.Payment{DealID: deal.ID, Amount: form.Amount, PaidAt: paidAt, Method: form.Method, Note: form.Note}
	if err := h.DB.Create(&payment).Error; err != nil {
		return utils.Internal(c, "Failed to create payment")
	}
	return utils.Created(c, payment)
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
	if err := h.DB.First(&payment, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Payment not found")
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(payment.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}
	if err := h.DB.Delete(&payment).Error; err != nil {
		return utils.Internal(c, "Failed to delete payment")
	}
	return utils.NoContent(c)
}

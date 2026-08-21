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

// List — GET /deals/:dealId/payments. Returns payments plus a computed total_paid.
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

// Create — POST /deals/:dealId/payments.
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

// Delete — DELETE /payments/:id (hard delete).
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

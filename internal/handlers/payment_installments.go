package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type PaymentInstallmentHandler struct {
	DB *gorm.DB
}

func NewPaymentInstallmentHandler(db *gorm.DB) *PaymentInstallmentHandler {
	return &PaymentInstallmentHandler{DB: db}
}

// installmentStatuses loads a Deal's installments and its actual Payments
// total, then runs the shared waterfall helper — same total-paid sum
// PaymentHandler.List already computes (payments.go:38-41), kept here as its
// own small query rather than calling that handler, since only the sum is
// needed.
func (h *PaymentInstallmentHandler) installmentStatuses(dealID uint) ([]utils.InstallmentStatus, error) {
	var installments []models.PaymentInstallment
	if err := h.DB.Where("deal_id = ?", dealID).Order("due_date").Find(&installments).Error; err != nil {
		return nil, err
	}

	var payments []models.Payment
	if err := h.DB.Where("deal_id = ?", dealID).Find(&payments).Error; err != nil {
		return nil, err
	}
	var totalPaid float64
	for _, p := range payments {
		totalPaid += p.Amount
	}

	return utils.ComputeInstallmentStatuses(installments, totalPaid, time.Now()), nil
}

// List godoc
// @Summary List a deal's payment installment schedule (Admin/Sales Rep/Sales Manager/Marketing)
// @Description Returns each planned installment with its derived paid/partial/overdue/upcoming status (utils.ComputeInstallmentStatuses), ordered by due_date. Backs the Deal detail page's Payment Schedule section, and is also served read-only to API keys at /open/deals/{dealId}/payment-installments. Sales Rep/Marketing callers only see Deals assigned to them or unassigned; Admin/Sales Manager see every Deal. api-system-spec.md §7.5a.
// @Tags payment-installments
// @Security BearerAuth
// @Security ApiKeyAuth
// @Produce json
// @Param dealId path int true "Deal ID"
// @Success 200 {array} utils.InstallmentStatus
// @Failure 403 {object} map[string]interface{} "Not authorized to read this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{dealId}/payment-installments [get]
// @Router /open/deals/{dealId}/payment-installments [get]
func (h *PaymentInstallmentHandler) List(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}
	statuses, err := h.installmentStatuses(deal.ID)
	if err != nil {
		return utils.Internal(c, "Failed to list payment installments")
	}
	return utils.OK(c, statuses)
}

type paymentInstallmentForm struct {
	Amount  float64    `json:"amount"`
	DueDate *time.Time `json:"due_date"`
	Note    string     `json:"note"`
}

func (f paymentInstallmentForm) validate(c *fiber.Ctx) bool {
	if f.Amount <= 0 {
		_ = utils.ValidationError(c, "amount is required", map[string][]string{"amount": {"required"}})
		return false
	}
	if f.DueDate == nil {
		_ = utils.ValidationError(c, "due_date is required", map[string][]string{"due_date": {"required"}})
		return false
	}
	return true
}

// Create godoc
// @Summary Add a planned installment to a deal's payment schedule (Admin/Sales Rep/Sales Manager)
// @Description amount must be > 0, due_date is required. No validation against the Deal's value or existing installments — permissive, matching Payment's own lack of a "can't exceed deal value" check. Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create.
// @Tags payment-installments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body paymentInstallmentForm true "Installment fields"
// @Success 201 {object} models.PaymentInstallment
// @Failure 400 {object} map[string]interface{} "Invalid request body, amount, or due_date"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{dealId}/payment-installments [post]
func (h *PaymentInstallmentHandler) Create(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form paymentInstallmentForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !form.validate(c) {
		return nil
	}

	installment := models.PaymentInstallment{DealID: deal.ID, Amount: form.Amount, DueDate: *form.DueDate, Note: form.Note}
	if err := h.DB.Create(&installment).Error; err != nil {
		return utils.Internal(c, "Failed to create payment installment")
	}
	return utils.Created(c, installment)
}

type paymentInstallmentBulkForm struct {
	Installments []paymentInstallmentForm `json:"installments"`
}

// BulkCreate godoc
// @Summary Generate a payment schedule in one action (Admin/Sales Rep/Sales Manager)
// @Description Creates every installment in one batch insert + one summary audit-log entry, instead of the caller looping N calls to Create — mirrors CampaignHandler.BulkCreateTasks's shape. The frontend computes the actual split (equal amounts, spaced dates); this endpoint only validates and inserts, same permissive per-row rules as the single-row Create. Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create.
// @Tags payment-installments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body paymentInstallmentBulkForm true "Installments to create"
// @Success 201 {array} models.PaymentInstallment
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{dealId}/payment-installments/bulk [post]
func (h *PaymentInstallmentHandler) BulkCreate(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form paymentInstallmentBulkForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.Installments) == 0 {
		return utils.ValidationError(c, "installments is required", map[string][]string{"installments": {"required"}})
	}
	for _, row := range form.Installments {
		if !row.validate(c) {
			return nil
		}
	}

	installments := make([]models.PaymentInstallment, 0, len(form.Installments))
	for _, row := range form.Installments {
		installments = append(installments, models.PaymentInstallment{
			DealID: deal.ID, Amount: row.Amount, DueDate: *row.DueDate, Note: row.Note,
		})
	}

	actorID := middleware.CurrentUserID(c)
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&installments).Error; err != nil {
			return err
		}
		after := models.JSONMap{"deal_id": deal.ID, "installment_count": len(installments)}
		return utils.WriteAuditLog(tx, "deal", deal.ID, "bulk_created_payment_installments", nil, after, actorID)
	})
	if err != nil {
		return utils.Internal(c, "Failed to generate payment schedule")
	}
	return utils.Created(c, installments)
}

// Update godoc
// @Summary Edit a planned installment (Admin/Sales Rep/Sales Manager)
// @Description Same validation as Create. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may edit.
// @Tags payment-installments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Payment Installment ID"
// @Param body body paymentInstallmentForm true "Installment fields"
// @Success 200 {object} models.PaymentInstallment
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Payment installment not found, or deal not found"
// @Router /payment-installments/{id} [put]
func (h *PaymentInstallmentHandler) Update(c *fiber.Ctx) error {
	var installment models.PaymentInstallment
	if err := utils.FindByID(c, h.DB, &installment, "Payment installment not found"); err != nil {
		return nil
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(installment.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form paymentInstallmentForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !form.validate(c) {
		return nil
	}

	installment.Amount, installment.DueDate, installment.Note = form.Amount, *form.DueDate, form.Note
	if err := h.DB.Save(&installment).Error; err != nil {
		return utils.Internal(c, "Failed to update payment installment")
	}
	return utils.OK(c, installment)
}

// Delete godoc
// @Summary Delete a planned installment
// @Description Hard delete. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may delete.
// @Tags payment-installments
// @Security BearerAuth
// @Param id path int true "Payment Installment ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Payment installment not found, or deal not found"
// @Router /payment-installments/{id} [delete]
func (h *PaymentInstallmentHandler) Delete(c *fiber.Ctx) error {
	var installment models.PaymentInstallment
	if err := utils.FindByID(c, h.DB, &installment, "Payment installment not found"); err != nil {
		return nil
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(installment.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}
	if err := h.DB.Delete(&installment).Error; err != nil {
		return utils.Internal(c, "Failed to delete payment installment")
	}
	return utils.NoContent(c)
}

package handlers

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ContractHandler struct {
	DB *gorm.DB
}

func NewContractHandler(db *gorm.DB) *ContractHandler {
	return &ContractHandler{DB: db}
}

// List — GET /deals/:dealId/contracts.
func (h *ContractHandler) List(c *fiber.Ctx) error {
	var contracts []models.Contract
	if err := h.DB.Where("deal_id = ?", c.Params("dealId")).Order("created_at DESC").Find(&contracts).Error; err != nil {
		return utils.Internal(c, "Failed to list contracts")
	}
	return utils.OK(c, contracts)
}

type contractForm struct {
	Status  models.ContractStatus `json:"status"`
	QuoteID *uint                 `json:"quote_id"`
}

// Create — POST /deals/:dealId/contracts.
func (h *ContractHandler) Create(c *fiber.Ctx) error {
	deal, err := dealForSubResource(c, h.DB, c.Params("dealId"))
	if err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form contractForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	contract := models.Contract{DealID: deal.ID, Status: form.Status, QuoteID: form.QuoteID}
	if contract.Status == "" {
		contract.Status = models.ContractStatusDraft
	}
	if err := h.DB.Create(&contract).Error; err != nil {
		return utils.Internal(c, "Failed to create contract")
	}
	return utils.Created(c, contract)
}

// Update — PUT /contracts/:id. Update status.
func (h *ContractHandler) Update(c *fiber.Ctx) error {
	var contract models.Contract
	if err := h.DB.First(&contract, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contract not found")
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(contract.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	var form contractForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Status != "" {
		contract.Status = form.Status
	}
	if form.QuoteID != nil {
		contract.QuoteID = form.QuoteID
	}

	if err := h.DB.Save(&contract).Error; err != nil {
		return utils.Internal(c, "Failed to update contract")
	}
	return utils.OK(c, contract)
}

// Upload — POST /contracts/:id/upload. Uploads the signed document, sets
// signed_file_url/signed_date.
func (h *ContractHandler) Upload(c *fiber.Ctx) error {
	var contract models.Contract
	if err := h.DB.First(&contract, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contract not found")
	}
	if _, err := dealForSubResource(c, h.DB, fmt.Sprint(contract.DealID)); err != nil {
		return respondFindErr(c, err, "Deal not found")
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return utils.BadRequest(c, "Missing file")
	}
	fileURL, _, err := utils.SaveUpload(c, fh)
	if err != nil {
		return utils.RespondUploadError(c, err)
	}

	now := time.Now()
	contract.SignedFileURL = &fileURL
	contract.SignedDate = &now
	contract.Status = models.ContractStatusSigned
	if err := h.DB.Save(&contract).Error; err != nil {
		return utils.Internal(c, "Failed to update contract")
	}
	return utils.OK(c, contract)
}

// ExportPDF — GET /contracts/:id/export-pdf. Renders a plain (unbranded, same
// style as QuoteHandler.ExportPDF) PDF: party details (Company legal
// name/address/tax ID, Contact name/role), Deal info, the linked Quote's
// scope_of_work and line items/total (if quote_id is set), status, and a
// signature-line placeholder. Read-only, same access level as List (no
// CanWrite check).
func (h *ContractHandler) ExportPDF(c *fiber.Ctx) error {
	var contract models.Contract
	if err := h.DB.First(&contract, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contract not found")
	}
	var deal models.Deal
	if err := h.DB.First(&deal, contract.DealID).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	var company models.Company
	h.DB.First(&company, deal.CompanyID)
	var contact models.Contact
	h.DB.First(&contact, deal.ContactID)

	var quote *models.Quote
	if contract.QuoteID != nil {
		var q models.Quote
		if err := h.DB.First(&q, *contract.QuoteID).Error; err == nil {
			quote = &q
		}
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, "Contract")
	pdf.Ln(12)

	pdf.SetFont("Arial", "", 11)
	pdf.Cell(0, 6, fmt.Sprintf("Deal: %s", deal.Title))
	pdf.Ln(6)
	pdf.Cell(0, 6, fmt.Sprintf("Party: %s", strOrDefault(company.LegalName, company.Name)))
	pdf.Ln(6)
	if company.Address != nil && *company.Address != "" {
		pdf.Cell(0, 6, fmt.Sprintf("Address: %s", *company.Address))
		pdf.Ln(6)
	}
	if company.TaxID != nil && *company.TaxID != "" {
		pdf.Cell(0, 6, fmt.Sprintf("Tax ID: %s", *company.TaxID))
		pdf.Ln(6)
	}
	pdf.Cell(0, 6, fmt.Sprintf("Contact: %s (%s)", contact.Name, contact.RoleTitle))
	pdf.Ln(6)
	pdf.Cell(0, 6, fmt.Sprintf("Status: %s", contract.Status))
	pdf.Ln(6)
	if contract.SignedDate != nil {
		pdf.Cell(0, 6, fmt.Sprintf("Signed Date: %s", contract.SignedDate.Format("2006-01-02")))
		pdf.Ln(6)
	}
	pdf.Ln(4)

	if quote != nil {
		if quote.ScopeOfWork != "" {
			pdf.SetFont("Arial", "B", 11)
			pdf.Cell(0, 6, "Scope of Work")
			pdf.Ln(7)
			pdf.SetFont("Arial", "", 10)
			pdf.MultiCell(0, 5, quote.ScopeOfWork, "", "L", false)
			pdf.Ln(4)
		}
		utils.RenderLineItemsTable(pdf, quote.Items)
		pdf.Ln(16)
	} else {
		pdf.SetFont("Arial", "I", 10)
		pdf.Cell(0, 6, "No linked quote — pricing not included.")
		pdf.Ln(16)
	}

	pdf.SetFont("Arial", "", 10)
	pdf.Cell(85, 6, "___________________________")
	pdf.Cell(10, 6, "")
	pdf.Cell(85, 6, "___________________________")
	pdf.Ln(6)
	pdf.Cell(85, 6, "Company Signature")
	pdf.Cell(10, 6, "")
	pdf.Cell(85, 6, "Customer Signature")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return utils.Internal(c, "Failed to generate PDF")
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="contract-%d.pdf"`, contract.ID))
	return c.Send(buf.Bytes())
}

// strOrDefault returns *s if non-nil and non-empty, else def.
func strOrDefault(s *string, def string) string {
	if s != nil && *s != "" {
		return *s
	}
	return def
}

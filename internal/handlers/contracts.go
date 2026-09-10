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
	DB      *gorm.DB
	Storage utils.Storage
}

func NewContractHandler(db *gorm.DB, storage utils.Storage) *ContractHandler {
	return &ContractHandler{DB: db, Storage: storage}
}

// List godoc
// @Summary List contracts for a deal (Admin/Sales Rep/Sales Manager)
// @Description Returns contracts for a Deal, ordered newest first. api-system-spec.md §8.1.
// @Tags contracts
// @Security BearerAuth
// @Produce json
// @Param dealId path int true "Deal ID"
// @Success 200 {array} models.Contract
// @Router /deals/{dealId}/contracts [get]
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

// Create godoc
// @Summary Create a contract (Admin/Sales Rep/Sales Manager)
// @Description Creates a Contract on a Deal, optionally linked to a Quote (quote_id) for PDF line items. status defaults to draft. Only the Deal's assigned Sales Rep (or Admin/Sales Manager) may create. api-system-spec.md §8.1.
// @Tags contracts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param dealId path int true "Deal ID"
// @Param body body contractForm true "Contract fields"
// @Success 201 {object} models.Contract
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{dealId}/contracts [post]
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

// Update godoc
// @Summary Update a contract
// @Description Updates status and/or quote_id. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may update. api-system-spec.md §8.1.
// @Tags contracts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Contract ID"
// @Param body body contractForm true "Contract fields"
// @Success 200 {object} models.Contract
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Contract not found, or deal not found"
// @Router /contracts/{id} [put]
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

// Upload godoc
// @Summary Upload a signed contract document
// @Description Uploads the signed document, sets signed_file_url/signed_date, and sets status to Signed. Only the parent Deal's assigned Sales Rep (or Admin/Sales Manager) may upload. api-system-spec.md §8.1.
// @Tags contracts
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path int true "Contract ID"
// @Param file formData file true "Signed contract file"
// @Success 200 {object} models.Contract
// @Failure 400 {object} map[string]interface{} "Missing file, or unsupported file type"
// @Failure 403 {object} map[string]interface{} "Not authorized to modify this deal's records"
// @Failure 404 {object} map[string]interface{} "Contract not found, or deal not found"
// @Failure 413 {object} map[string]interface{} "File exceeds 10MB limit"
// @Router /contracts/{id}/upload [post]
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
	key, _, err := h.Storage.Save(fh)
	if err != nil {
		return utils.RespondUploadError(c, err)
	}
	fileURL := "/uploads/" + key

	now := time.Now()
	contract.SignedFileURL = &fileURL
	contract.SignedDate = &now
	contract.Status = models.ContractStatusSigned
	if err := h.DB.Save(&contract).Error; err != nil {
		return utils.Internal(c, "Failed to update contract")
	}
	return utils.OK(c, contract)
}

// ExportPDF godoc
// @Summary Export a contract as PDF
// @Description Renders a plain (unbranded, same style as the Quote export) PDF: party details (Company legal name/address/tax ID, Contact name/role), Deal info, the linked Quote's scope_of_work and line items/total (if quote_id is set), status, and a signature-line placeholder. Read-only, same access level as List (no CanWrite ownership check). api-system-spec.md §8.1.
// @Tags contracts
// @Security BearerAuth
// @Produce application/pdf
// @Param id path int true "Contract ID"
// @Success 200 {file} file
// @Failure 404 {object} map[string]interface{} "Contract not found, or deal not found"
// @Router /contracts/{id}/export-pdf [get]
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

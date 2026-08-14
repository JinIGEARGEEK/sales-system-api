package handlers

import (
	"time"

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
	Status models.ContractStatus `json:"status"`
}

// Create — POST /deals/:dealId/contracts.
func (h *ContractHandler) Create(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("dealId")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}

	var form contractForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	contract := models.Contract{DealID: deal.ID, Status: form.Status}
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

	var form contractForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Status != "" {
		contract.Status = form.Status
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

	fh, err := c.FormFile("file")
	if err != nil {
		return utils.BadRequest(c, "Missing file")
	}
	fileURL, _, err := utils.SaveUpload(c, fh)
	if err != nil {
		if err.Error() == "file too large" {
			return utils.ErrorResponse(c, fiber.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "File exceeds 10MB limit")
		}
		return utils.Internal(c, "Failed to save file")
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

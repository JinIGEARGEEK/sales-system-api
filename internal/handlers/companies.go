package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type CompanyHandler struct {
	DB *gorm.DB
}

func NewCompanyHandler(db *gorm.DB) *CompanyHandler {
	return &CompanyHandler{DB: db}
}

// List — GET /companies. Filters: status, tag, industry, search (name).
func (h *CompanyHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := applyCompanyFilters(h.DB.Model(&models.Company{}), c)

	var total int64
	query.Count(&total)

	var companies []models.Company
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true, "industry": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&companies).Error; err != nil {
		return utils.Internal(c, "Failed to list companies")
	}
	return utils.List(c, companies, page, perPage, total)
}

type companyForm struct {
	Name        string   `json:"name"`
	Industry    string   `json:"industry"`
	Size        string   `json:"size"`
	RevenueSize string   `json:"revenue_size"`
	Website     string   `json:"website"`
	Tags        []string `json:"tags"`
	Notes       string   `json:"notes"`
	Status      string   `json:"status"`
	LegalName   *string  `json:"legal_name"`
	Address     *string  `json:"address"`
	TaxID       *string  `json:"tax_id"`
}

// Create — POST /companies.
func (h *CompanyHandler) Create(c *fiber.Ctx) error {
	var form companyForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if !utils.IsActiveIndustry(h.DB, form.Industry) {
		return utils.ValidationError(c, "industry is not a valid active industry", map[string][]string{"industry": {"invalid"}})
	}
	if !utils.IsActiveCompanySize(h.DB, form.Size) {
		return utils.ValidationError(c, "size is not a valid active company size", map[string][]string{"size": {"invalid"}})
	}
	if !utils.IsActiveRevenueSize(h.DB, form.RevenueSize) {
		return utils.ValidationError(c, "revenue_size is not a valid active revenue size", map[string][]string{"revenue_size": {"invalid"}})
	}

	actorID := middleware.CurrentUserID(c)
	company := models.Company{
		Name: form.Name, Industry: form.Industry, Size: form.Size, RevenueSize: form.RevenueSize, Website: form.Website,
		Domain: utils.ExtractDomain(form.Website),
		Tags:   pq.StringArray(form.Tags), Notes: form.Notes,
		Status:    models.ActiveArchivedStatus(form.Status),
		LegalName: form.LegalName, Address: form.Address, TaxID: form.TaxID,
	}
	if company.Status == "" {
		company.Status = models.StatusActive
	}
	company.CreatedBy = &actorID
	company.UpdatedBy = &actorID
	if err := h.DB.Create(&company).Error; err != nil {
		return utils.Internal(c, "Failed to create company")
	}
	return utils.Created(c, company)
}

// Get — GET /companies/:id.
func (h *CompanyHandler) Get(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}
	return utils.OK(c, company)
}

// Update — PUT /companies/:id.
func (h *CompanyHandler) Update(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}

	var form companyForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.IsActiveIndustry(h.DB, form.Industry) {
		return utils.ValidationError(c, "industry is not a valid active industry", map[string][]string{"industry": {"invalid"}})
	}
	if !utils.IsActiveCompanySize(h.DB, form.Size) {
		return utils.ValidationError(c, "size is not a valid active company size", map[string][]string{"size": {"invalid"}})
	}
	if !utils.IsActiveRevenueSize(h.DB, form.RevenueSize) {
		return utils.ValidationError(c, "revenue_size is not a valid active revenue size", map[string][]string{"revenue_size": {"invalid"}})
	}

	company.Name, company.Industry, company.Size, company.RevenueSize, company.Website = form.Name, form.Industry, form.Size, form.RevenueSize, form.Website
	company.Domain = utils.ExtractDomain(form.Website)
	company.Tags = pq.StringArray(form.Tags)
	company.Notes = form.Notes
	company.LegalName, company.Address, company.TaxID = form.LegalName, form.Address, form.TaxID
	if form.Status != "" {
		company.Status = models.ActiveArchivedStatus(form.Status)
	}
	actorID := middleware.CurrentUserID(c)
	company.UpdatedBy = &actorID

	if err := h.DB.Save(&company).Error; err != nil {
		return utils.Internal(c, "Failed to update company")
	}
	return utils.OK(c, company)
}

// Delete — DELETE /companies/:id. Soft-delete (AuditedModel) — recoverable via
// Restore/Trash below. Never a hard delete, since Deals/Contacts/Payments
// reference company_id.
func (h *CompanyHandler) Delete(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}
	actorID := middleware.CurrentUserID(c)
	if err := utils.GenericSoftDelete(h.DB, &company, actorID); err != nil {
		return utils.Internal(c, "Failed to delete company")
	}
	return utils.NoContent(c)
}

// Trash — GET /companies/trash. Sales-Manager/Admin only (route-gated).
func (h *CompanyHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Company](c, h.DB, "Failed to list deleted companies")
}

// Restore — POST /companies/:id/restore. Sales-Manager/Admin only (route-gated).
func (h *CompanyHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Company](c, h.DB, "Deleted company not found", "Failed to restore company")
}

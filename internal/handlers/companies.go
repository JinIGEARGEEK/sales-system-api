package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"gorm.io/gorm"

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
	query := h.DB.Model(&models.Company{})

	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("industry"); v != "" {
		query = query.Where("industry = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		query = query.Where("? = ANY(tags)", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("name ILIKE ?", "%"+v+"%")
	}

	var total int64
	query.Count(&total)

	var companies []models.Company
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&companies).Error; err != nil {
		return utils.Internal(c, "Failed to list companies")
	}
	return utils.List(c, companies, page, perPage, total)
}

type companyForm struct {
	Name     string   `json:"name"`
	Industry string   `json:"industry"`
	Size     string   `json:"size"`
	Website  string   `json:"website"`
	Tags     []string `json:"tags"`
	Notes    string   `json:"notes"`
	Status   string   `json:"status"`
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

	company := models.Company{
		Name: form.Name, Industry: form.Industry, Size: form.Size, Website: form.Website,
		Tags: pq.StringArray(form.Tags), Notes: form.Notes,
		Status: models.ActiveArchivedStatus(form.Status),
	}
	if company.Status == "" {
		company.Status = models.StatusActive
	}
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

	company.Name, company.Industry, company.Size, company.Website = form.Name, form.Industry, form.Size, form.Website
	company.Tags = pq.StringArray(form.Tags)
	company.Notes = form.Notes
	if form.Status != "" {
		company.Status = models.ActiveArchivedStatus(form.Status)
	}

	if err := h.DB.Save(&company).Error; err != nil {
		return utils.Internal(c, "Failed to update company")
	}
	return utils.OK(c, company)
}

// Delete — DELETE /companies/:id. Sets status: 'archived' (soft delete, §1.6) —
// never a hard delete, since Deals/Contacts/Payments reference company_id.
func (h *CompanyHandler) Delete(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}
	company.Status = models.StatusArchived
	if err := h.DB.Save(&company).Error; err != nil {
		return utils.Internal(c, "Failed to archive company")
	}
	return utils.NoContent(c)
}

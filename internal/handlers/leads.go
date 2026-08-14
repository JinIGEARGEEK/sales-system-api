package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type LeadHandler struct {
	DB *gorm.DB
}

func NewLeadHandler(db *gorm.DB) *LeadHandler {
	return &LeadHandler{DB: db}
}

// List — GET /leads. Filters: status, source, assigned_to, search (name/company_name/email).
func (h *LeadHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Lead{})

	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("source"); v != "" {
		query = query.Where("source = ?", v)
	}
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		query = query.Where("name ILIKE ? OR company_name ILIKE ? OR email ILIKE ?", like, like, like)
	}

	var total int64
	query.Count(&total)

	var leads []models.Lead
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&leads).Error; err != nil {
		return utils.Internal(c, "Failed to list leads")
	}
	return utils.List(c, leads, page, perPage, total)
}

type leadForm struct {
	Name        string             `json:"name"`
	CompanyName string             `json:"company_name"`
	Email       string             `json:"email"`
	Phone       string             `json:"phone"`
	Source      models.LeadSource  `json:"source"`
	Status      models.LeadStatus  `json:"status"`
	Notes       string             `json:"notes"`
	AssignedTo  *uint              `json:"assigned_to"`
}

// Create — POST /leads.
func (h *LeadHandler) Create(c *fiber.Ctx) error {
	var form leadForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a lead to another sales rep")
	}

	lead := models.Lead{
		Name: form.Name, CompanyName: form.CompanyName, Email: form.Email, Phone: form.Phone,
		Source: form.Source, Status: form.Status, Notes: form.Notes, AssignedTo: form.AssignedTo,
	}
	if lead.Status == "" {
		lead.Status = models.LeadStatusNew
	}
	if err := h.DB.Create(&lead).Error; err != nil {
		return utils.Internal(c, "Failed to create lead")
	}
	return utils.Created(c, lead)
}

// Get — GET /leads/:id.
func (h *LeadHandler) Get(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	return utils.OK(c, lead)
}

// Update — PUT /leads/:id (including status transitions).
func (h *LeadHandler) Update(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this lead")
	}

	var form leadForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a lead to another sales rep")
	}

	lead.Name, lead.CompanyName, lead.Email, lead.Phone = form.Name, form.CompanyName, form.Email, form.Phone
	lead.Source, lead.Status, lead.Notes, lead.AssignedTo = form.Source, form.Status, form.Notes, form.AssignedTo

	if err := h.DB.Save(&lead).Error; err != nil {
		return utils.Internal(c, "Failed to update lead")
	}
	return utils.OK(c, lead)
}

// Delete — DELETE /leads/:id.
func (h *LeadHandler) Delete(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this lead")
	}
	if err := h.DB.Delete(&lead).Error; err != nil {
		return utils.Internal(c, "Failed to delete lead")
	}
	return utils.NoContent(c)
}

type convertRequest struct {
	CompanyID *uint `json:"company_id"`
	ContactID *uint `json:"contact_id"`
	Deal      struct {
		Title             string             `json:"title"`
		Value             float64            `json:"value"`
		Stage             models.DealStage   `json:"stage"`
		ExpectedCloseDate *string            `json:"expected_close_date"`
		AssignedTo        *uint              `json:"assigned_to"`
		Channel           models.LeadSource  `json:"channel"`
		BusinessUnit      *models.BusinessUnit `json:"business_unit"`
		BusinessUnitItem  *string            `json:"business_unit_item"`
	} `json:"deal"`
}

// Convert — POST /leads/:id/convert. Converts a Qualified Lead into a Deal
// (and Company/Contact if new) — FR-CRM-004, api-system-spec.md §3.
func (h *LeadHandler) Convert(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}

	var req convertRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	var company models.Company
	var contact models.Contact
	var deal models.Deal

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if req.CompanyID != nil {
			if err := tx.First(&company, *req.CompanyID).Error; err != nil {
				return err
			}
		} else {
			company = models.Company{Name: lead.CompanyName, Status: models.StatusActive}
			if err := tx.Create(&company).Error; err != nil {
				return err
			}
		}

		if req.ContactID != nil {
			if err := tx.First(&contact, *req.ContactID).Error; err != nil {
				return err
			}
		} else {
			contact = models.Contact{
				CompanyID: company.ID, Name: lead.Name, Email: lead.Email, Phone: lead.Phone,
				Status: models.StatusActive,
			}
			if err := tx.Create(&contact).Error; err != nil {
				return err
			}
		}

		deal = models.Deal{
			CompanyID: company.ID, ContactID: contact.ID,
			Title: req.Deal.Title, Value: req.Deal.Value, Stage: req.Deal.Stage,
			Status: models.DealStatusOpen, ExpectedCloseDate: req.Deal.ExpectedCloseDate,
			AssignedTo: req.Deal.AssignedTo, Channel: req.Deal.Channel,
			BusinessUnit: req.Deal.BusinessUnit, BusinessUnitItem: req.Deal.BusinessUnitItem,
		}
		if deal.Title == "" {
			deal.Title = lead.Name
		}
		if deal.Stage == "" {
			deal.Stage = models.DealStageQualified
		}
		if err := tx.Create(&deal).Error; err != nil {
			return err
		}

		lead.Status = models.LeadStatusQualified
		return tx.Save(&lead).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to convert lead")
	}

	return utils.OK(c, fiber.Map{"deal": deal, "company": company, "contact": contact})
}

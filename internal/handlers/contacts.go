package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ContactHandler struct {
	DB *gorm.DB
}

func NewContactHandler(db *gorm.DB) *ContactHandler {
	return &ContactHandler{DB: db}
}

// List — GET /contacts. Filters: company_id, status, tag, search (name/email).
func (h *ContactHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Contact{})

	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		query = query.Where("? = ANY(tags)", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		query = query.Where("name ILIKE ? OR email ILIKE ?", like, like)
	}

	var total int64
	query.Count(&total)

	var contacts []models.Contact
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&contacts).Error; err != nil {
		return utils.Internal(c, "Failed to list contacts")
	}
	return utils.List(c, contacts, page, perPage, total)
}

type contactForm struct {
	CompanyID uint     `json:"company_id"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	Phone     string   `json:"phone"`
	RoleTitle string   `json:"role_title"`
	Tags      []string `json:"tags"`
	Status    string   `json:"status"`
}

// Create — POST /contacts.
func (h *ContactHandler) Create(c *fiber.Ctx) error {
	var form contactForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" || form.CompanyID == 0 {
		return utils.ValidationError(c, "company_id and name are required", map[string][]string{
			"company_id": {"required"},
			"name":       {"required"},
		})
	}

	contact := models.Contact{
		CompanyID: form.CompanyID, Name: form.Name, Email: form.Email, Phone: form.Phone,
		RoleTitle: form.RoleTitle, Tags: pq.StringArray(form.Tags),
		Status: models.ActiveArchivedStatus(form.Status),
	}
	if contact.Status == "" {
		contact.Status = models.StatusActive
	}
	if err := h.DB.Create(&contact).Error; err != nil {
		return utils.Internal(c, "Failed to create contact")
	}
	return utils.Created(c, contact)
}

// Get — GET /contacts/:id.
func (h *ContactHandler) Get(c *fiber.Ctx) error {
	var contact models.Contact
	if err := h.DB.First(&contact, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contact not found")
	}
	return utils.OK(c, contact)
}

// Update — PUT /contacts/:id.
func (h *ContactHandler) Update(c *fiber.Ctx) error {
	var contact models.Contact
	if err := h.DB.First(&contact, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contact not found")
	}

	var form contactForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	if form.CompanyID != 0 {
		contact.CompanyID = form.CompanyID
	}
	contact.Name, contact.Email, contact.Phone, contact.RoleTitle = form.Name, form.Email, form.Phone, form.RoleTitle
	contact.Tags = pq.StringArray(form.Tags)
	if form.Status != "" {
		contact.Status = models.ActiveArchivedStatus(form.Status)
	}

	if err := h.DB.Save(&contact).Error; err != nil {
		return utils.Internal(c, "Failed to update contact")
	}
	return utils.OK(c, contact)
}

// Delete — DELETE /contacts/:id. Soft-delete (status: 'archived'), never a hard
// delete, since Deal/Activity/Task records reference contact_id.
func (h *ContactHandler) Delete(c *fiber.Ctx) error {
	var contact models.Contact
	if err := h.DB.First(&contact, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contact not found")
	}
	contact.Status = models.StatusArchived
	if err := h.DB.Save(&contact).Error; err != nil {
		return utils.Internal(c, "Failed to archive contact")
	}
	return utils.NoContent(c)
}

package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
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
	query := applyContactFilters(h.DB.Model(&models.Contact{}), c)

	var total int64
	query.Count(&total)

	var contacts []models.Contact
	if joined, ok := utils.ApplyCompanyNameSort(query, "contacts", c.Query("sort")); ok {
		query = joined
	} else {
		query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true, "email": true}, "-created_at")
	}
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
	if !utils.IsActiveJobTitle(h.DB, form.RoleTitle) {
		return utils.ValidationError(c, "role_title is not a valid active job title", map[string][]string{"role_title": {"invalid"}})
	}

	actorID := middleware.CurrentUserID(c)
	contact := models.Contact{
		CompanyID: form.CompanyID, Name: form.Name, Email: form.Email, Phone: form.Phone,
		RoleTitle: form.RoleTitle, Tags: pq.StringArray(form.Tags),
		Status: models.ActiveArchivedStatus(form.Status),
	}
	if contact.Status == "" {
		contact.Status = models.StatusActive
	}
	contact.CreatedBy = &actorID
	contact.UpdatedBy = &actorID
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
	if !utils.IsActiveJobTitle(h.DB, form.RoleTitle) {
		return utils.ValidationError(c, "role_title is not a valid active job title", map[string][]string{"role_title": {"invalid"}})
	}

	if form.CompanyID != 0 {
		contact.CompanyID = form.CompanyID
	}
	contact.Name, contact.Email, contact.Phone, contact.RoleTitle = form.Name, form.Email, form.Phone, form.RoleTitle
	contact.Tags = pq.StringArray(form.Tags)
	if form.Status != "" {
		contact.Status = models.ActiveArchivedStatus(form.Status)
	}
	actorID := middleware.CurrentUserID(c)
	contact.UpdatedBy = &actorID

	if err := h.DB.Save(&contact).Error; err != nil {
		return utils.Internal(c, "Failed to update contact")
	}
	return utils.OK(c, contact)
}

// Delete — DELETE /contacts/:id. Soft-delete (AuditedModel) — recoverable via
// Restore/Trash below. Never a hard delete, since Deal/Activity/Task records
// reference contact_id.
func (h *ContactHandler) Delete(c *fiber.Ctx) error {
	var contact models.Contact
	if err := h.DB.First(&contact, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contact not found")
	}
	actorID := middleware.CurrentUserID(c)
	if err := utils.GenericSoftDelete(h.DB, &contact, actorID); err != nil {
		return utils.Internal(c, "Failed to delete contact")
	}
	return utils.NoContent(c)
}

// Trash — GET /contacts/trash. Sales-Manager/Admin only (route-gated).
func (h *ContactHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Contact](c, h.DB, "Failed to list deleted contacts")
}

// Restore — POST /contacts/:id/restore. Sales-Manager/Admin only (route-gated).
func (h *ContactHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Contact](c, h.DB, "Deleted contact not found", "Failed to restore contact")
}

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

// List godoc
// @Summary List contacts
// @Description Paginated, filterable Contact list. Filters: company_id, status, tag, search (name/email).
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param company_id query int false "Filter by Company ID"
// @Param status query string false "active or archived"
// @Param tag query string false "Filter by Contact tag"
// @Param search query string false "Search by name or email"
// @Param sort query string false "Sort field, prefix - for descending (created_at, name, email, company_name)"
// @Param page query int false "Page number"
// @Param per_page query int false "Items per page"
// @Success 200 {object} map[string]interface{} "Paginated contact list (data, page, per_page, total)"
// @Router /contacts [get]
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
	IsPrimary bool     `json:"is_primary"`
}

// clearOtherPrimaryContacts unsets IsPrimary on every other Contact in
// companyID so at most one Contact per Company is ever Primary (FR-CRM-012).
// exceptID is the Contact currently being saved as Primary (0 on Create,
// where the row doesn't exist yet).
func clearOtherPrimaryContacts(tx *gorm.DB, companyID uint, exceptID uint) error {
	return tx.Model(&models.Contact{}).
		Where("company_id = ? AND id <> ? AND is_primary = ?", companyID, exceptID, true).
		Update("is_primary", false).Error
}

// Create godoc
// @Summary Create a contact
// @Description Creates a Contact. company_id and name are required; role_title must match an active configured job title (see /admin/job-titles).
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body contactForm true "Contact fields"
// @Success 201 {object} models.Contact
// @Failure 400 {object} map[string]interface{} "Invalid body, missing fields, or invalid role_title"
// @Router /contacts [post]
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
		Status: models.ActiveArchivedStatus(form.Status), IsPrimary: form.IsPrimary,
	}
	if contact.Status == "" {
		contact.Status = models.StatusActive
	}
	contact.CreatedBy = &actorID
	contact.UpdatedBy = &actorID
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&contact).Error; err != nil {
			return err
		}
		if contact.IsPrimary {
			return clearOtherPrimaryContacts(tx, contact.CompanyID, contact.ID)
		}
		return nil
	})
	if err != nil {
		return utils.Internal(c, "Failed to create contact")
	}
	return utils.Created(c, contact)
}

// Get godoc
// @Summary Get a contact
// @Description Returns a single Contact.
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param id path int true "Contact ID"
// @Success 200 {object} models.Contact
// @Failure 404 {object} map[string]interface{} "Contact not found"
// @Router /contacts/{id} [get]
func (h *ContactHandler) Get(c *fiber.Ctx) error {
	var contact models.Contact
	if err := h.DB.First(&contact, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Contact not found")
	}
	return utils.OK(c, contact)
}

// Update godoc
// @Summary Update a contact
// @Description Updates a Contact. role_title must match an active configured job title.
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Contact ID"
// @Param body body contactForm true "Contact fields"
// @Success 200 {object} models.Contact
// @Failure 400 {object} map[string]interface{} "Invalid body or invalid role_title"
// @Failure 404 {object} map[string]interface{} "Contact not found"
// @Router /contacts/{id} [put]
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
	contact.IsPrimary = form.IsPrimary
	actorID := middleware.CurrentUserID(c)
	contact.UpdatedBy = &actorID

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&contact).Error; err != nil {
			return err
		}
		if contact.IsPrimary {
			return clearOtherPrimaryContacts(tx, contact.CompanyID, contact.ID)
		}
		return nil
	})
	if err != nil {
		return utils.Internal(c, "Failed to update contact")
	}
	return utils.OK(c, contact)
}

// Delete godoc
// @Summary Delete a contact
// @Description Soft-delete (AuditedModel) — recoverable via Restore/Trash below. Never a hard delete, since Deal/Activity/Task records reference contact_id.
// @Tags contacts
// @Security BearerAuth
// @Param id path int true "Contact ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{} "Contact not found"
// @Router /contacts/{id} [delete]
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

// Trash godoc
// @Summary List deleted contacts (Admin/Sales Manager only)
// @Description Returns soft-deleted Contacts.
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param search query string false "Search by name"
// @Success 200 {object} map[string]interface{} "Paginated contact list (data, page, per_page, total)"
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Router /contacts/trash [get]
func (h *ContactHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Contact](c, h.DB, "Failed to list deleted contacts", "name")
}

// Restore godoc
// @Summary Restore a deleted contact (Admin/Sales Manager only)
// @Description Un-deletes a soft-deleted Contact.
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param id path int true "Contact ID"
// @Success 200 {object} models.Contact
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Failure 404 {object} map[string]interface{} "Deleted contact not found"
// @Router /contacts/{id}/restore [post]
func (h *ContactHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Contact](c, h.DB, "Deleted contact not found", "Failed to restore contact")
}

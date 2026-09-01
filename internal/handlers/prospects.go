package handlers

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ProspectHandler struct {
	DB *gorm.DB
}

func NewProspectHandler(db *gorm.DB) *ProspectHandler {
	return &ProspectHandler{DB: db}
}

// List — GET /prospects. Filters: status, source, assigned_to, company_id
// (exact match), search (name/email/company name). Mirrors LeadHandler.List.
func (h *ProspectHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Prospect{})

	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("source"); v != "" {
		query = query.Where("source = ?", v)
	}
	if v := c.Query("assigned_to"); v == "unassigned" {
		query = query.Where("assigned_to IS NULL")
	} else if v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("prospects.company_id = ?", v)
	}

	sortField := strings.TrimPrefix(c.Query("sort"), "-")
	search := c.Query("search")
	query, needsCompanyJoin := utils.ApplyNullableCompanySearch(query, "prospects", sortField, search)
	if c.Query("exclude_converted") == "true" {
		query = query.Where("converted_lead_id IS NULL")
	}

	var total int64
	query.Count(&total)

	var prospects []models.Prospect
	if needsCompanyJoin {
		query = utils.ApplyNullableCompanySort(query, "prospects", c.Query("sort"), sortField)
	} else {
		query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	}
	if err := query.Limit(perPage).Offset(offset).Find(&prospects).Error; err != nil {
		return utils.Internal(c, "Failed to list prospects")
	}
	return utils.List(c, prospects, page, perPage, total)
}

type prospectForm struct {
	Name       string                `json:"name"`
	CompanyID  *uint                 `json:"company_id"`
	Email      string                `json:"email"`
	Phone      string                `json:"phone"`
	Source     models.LeadSource     `json:"source"`
	Status     models.ProspectStatus `json:"status"`
	Notes      string                `json:"notes"`
	AssignedTo *uint                 `json:"assigned_to"`
}

// Create — POST /prospects.
func (h *ProspectHandler) Create(c *fiber.Ctx) error {
	var form prospectForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a prospect to another team member")
	}
	if !utils.IsActiveLeadSource(h.DB, string(form.Source)) {
		return utils.ValidationError(c, "source is not a valid active lead source", map[string][]string{"source": {"invalid"}})
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}

	prospect := models.Prospect{
		Name: form.Name, CompanyID: form.CompanyID, Email: form.Email, Phone: form.Phone,
		Source: form.Source, Status: form.Status, Notes: form.Notes, AssignedTo: form.AssignedTo,
	}
	if prospect.Status == "" {
		prospect.Status = models.ProspectStatusNew
	}
	if err := h.DB.Create(&prospect).Error; err != nil {
		return utils.Internal(c, "Failed to create prospect")
	}
	return utils.Created(c, prospect)
}

// Get — GET /prospects/:id.
func (h *ProspectHandler) Get(c *fiber.Ctx) error {
	var prospect models.Prospect
	if err := h.DB.First(&prospect, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect not found")
	}
	return utils.OK(c, prospect)
}

// Update — PUT /prospects/:id (including status transitions).
func (h *ProspectHandler) Update(c *fiber.Ctx) error {
	var prospect models.Prospect
	if err := h.DB.First(&prospect, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect not found")
	}
	if !CanWrite(c, prospect.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this prospect")
	}

	var form prospectForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a prospect to another team member")
	}
	if !utils.IsActiveLeadSource(h.DB, string(form.Source)) {
		return utils.ValidationError(c, "source is not a valid active lead source", map[string][]string{"source": {"invalid"}})
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}

	prospect.Name, prospect.CompanyID, prospect.Email, prospect.Phone = form.Name, form.CompanyID, form.Email, form.Phone
	prospect.Source, prospect.Status, prospect.Notes, prospect.AssignedTo = form.Source, form.Status, form.Notes, form.AssignedTo

	if err := h.DB.Save(&prospect).Error; err != nil {
		return utils.Internal(c, "Failed to update prospect")
	}
	return utils.OK(c, prospect)
}

// Delete — DELETE /prospects/:id. Soft-delete (AuditedModel) — recoverable via
// Restore/Trash below.
func (h *ProspectHandler) Delete(c *fiber.Ctx) error {
	var prospect models.Prospect
	if err := h.DB.First(&prospect, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect not found")
	}
	if !CanWrite(c, prospect.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this prospect")
	}
	actorID := middleware.CurrentUserID(c)
	if err := utils.GenericSoftDelete(h.DB, &prospect, actorID); err != nil {
		return utils.Internal(c, "Failed to delete prospect")
	}
	return utils.NoContent(c)
}

// Trash — GET /prospects/trash. Sales-Manager/Admin only (route-gated).
func (h *ProspectHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Prospect](c, h.DB, "Failed to list deleted prospects")
}

// Restore — POST /prospects/:id/restore. Sales-Manager/Admin only (route-gated).
func (h *ProspectHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Prospect](c, h.DB, "Deleted prospect not found", "Failed to restore prospect")
}

// BulkReassign — PATCH /prospects/bulk-reassign. Sales-Manager/Admin only (route-gated).
func (h *ProspectHandler) BulkReassign(c *fiber.Ctx) error {
	var form bulkReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "prospect", "bulk_reassigned", actorID,
		func(tx *gorm.DB, prospect *models.Prospect) (models.JSONMap, models.JSONMap, error) {
			before := models.JSONMap{"assigned_to": prospect.AssignedTo}
			prospect.AssignedTo = form.AssignedTo
			after := models.JSONMap{"assigned_to": prospect.AssignedTo}
			return before, after, tx.Save(prospect).Error
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk reassign prospects")
	}
	return utils.NoContent(c)
}

// BulkTag — PATCH /prospects/bulk-tag. Sales-Manager/Admin only (route-gated).
func (h *ProspectHandler) BulkTag(c *fiber.Ctx) error {
	var form bulkTagForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "prospect", "bulk_tagged", actorID,
		func(tx *gorm.DB, prospect *models.Prospect) (models.JSONMap, models.JSONMap, error) {
			before := models.JSONMap{"tags": []string(prospect.Tags)}
			if form.Mode == "set" {
				prospect.Tags = form.Tags
			} else {
				prospect.Tags = mergeTags(prospect.Tags, form.Tags)
			}
			after := models.JSONMap{"tags": []string(prospect.Tags)}
			return before, after, tx.Save(prospect).Error
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk tag prospects")
	}
	return utils.NoContent(c)
}

// BulkArchive — PATCH /prospects/bulk-archive. Sales-Manager/Admin only (route-gated).
// Soft-deletes each prospect (same as Delete), in one transaction.
func (h *ProspectHandler) BulkArchive(c *fiber.Ctx) error {
	var form bulkIDsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "prospect", "bulk_archived", actorID,
		func(tx *gorm.DB, prospect *models.Prospect) (models.JSONMap, models.JSONMap, error) {
			if err := tx.Model(prospect).Update("deleted_by", actorID).Error; err != nil {
				return nil, nil, err
			}
			err := tx.Delete(prospect).Error
			return models.JSONMap{"deleted_at": nil}, models.JSONMap{"deleted_by": actorID}, err
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk archive prospects")
	}
	return utils.NoContent(c)
}

type prospectConvertRequest struct {
	CompanyID *uint `json:"company_id"`
	ContactID *uint `json:"contact_id"`
	Lead      struct {
		AssignedTo *uint `json:"assigned_to"`
	} `json:"lead"`
}

// Convert — POST /prospects/:id/convert. Converts a Prospect into a Lead (and
// Company/Contact if new) — mirrors LeadHandler.Convert's Lead→Deal pattern
// one funnel stage earlier: resolve-or-create Company → resolve-or-create
// Contact → create the target record with a back-reference → carry over
// Attachments → mark the source record converted, all in one transaction.
func (h *ProspectHandler) Convert(c *fiber.Ctx) error {
	var prospect models.Prospect
	if err := h.DB.First(&prospect, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect not found")
	}
	if !CanWrite(c, prospect.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to convert this prospect")
	}
	if prospect.ConvertedLeadID != nil {
		return utils.Conflict(c, "Prospect has already been converted")
	}

	var req prospectConvertRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	var company models.Company
	var contact models.Contact
	var lead models.Lead

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		switch {
		case req.CompanyID != nil:
			if err := tx.First(&company, *req.CompanyID).Error; err != nil {
				return err
			}
		case prospect.CompanyID != nil:
			if err := tx.First(&company, *prospect.CompanyID).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				company = models.Company{Status: models.StatusActive}
				if err := tx.Create(&company).Error; err != nil {
					return err
				}
			}
		default:
			company = models.Company{Status: models.StatusActive}
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
				CompanyID: company.ID, Name: prospect.Name, Email: prospect.Email, Phone: prospect.Phone,
				Status: models.StatusActive,
			}
			if err := tx.Create(&contact).Error; err != nil {
				return err
			}
		}

		lead = models.Lead{
			Name: prospect.Name, CompanyID: &company.ID, Email: prospect.Email, Phone: prospect.Phone,
			Source: prospect.Source, Status: models.LeadStatusNew, AssignedTo: req.Lead.AssignedTo,
			ProspectID: &prospect.ID,
		}
		if lead.AssignedTo == nil {
			lead.AssignedTo = prospect.AssignedTo
		}
		if err := tx.Create(&lead).Error; err != nil {
			return err
		}

		// Carry any Prospect attachments over to the new Lead rather than
		// leaving them stranded on a Prospect that's no longer actively
		// worked once converted — same reasoning as FR-CRM-090's Lead→Deal
		// carry-over.
		if err := tx.Model(&models.Attachment{}).
			Where("related_type = ? AND related_id = ?", models.AttachmentRelatedProspect, prospect.ID).
			Updates(map[string]interface{}{"related_type": models.AttachmentRelatedLead, "related_id": lead.ID}).Error; err != nil {
			return err
		}

		prospect.Status = models.ProspectStatusConverted
		prospect.ConvertedLeadID = &lead.ID
		return tx.Save(&prospect).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to convert prospect")
	}

	return utils.OK(c, fiber.Map{"lead": lead, "company": company, "contact": contact})
}

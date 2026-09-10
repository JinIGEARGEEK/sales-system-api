package handlers

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
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

// List godoc
// @Summary List prospects (Admin/Marketing/Sales Manager/Sales Rep)
// @Description Returns a paginated list of Prospects. Filters: status, source, assigned_to ("unassigned" for unassigned), company_id (exact match), search (name/email/company name), exclude_converted ("true" to hide already-converted Prospects). Mirrors LeadHandler.List.
// @Tags prospects
// @Security BearerAuth
// @Produce json
// @Param status query string false "Filter by status"
// @Param source query string false "Filter by source"
// @Param assigned_to query string false "Filter by assigned user ID, or \"unassigned\""
// @Param company_id query int false "Filter by Company ID"
// @Param search query string false "Search by name/email/company name"
// @Param exclude_converted query string false "Set to \"true\" to exclude already-converted prospects"
// @Param sort query string false "Sort field, prefix with - for descending (created_at, name)"
// @Success 200 {object} map[string]interface{} "Paginated prospect list (data, page, per_page, total)"
// @Router /prospects [get]
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

// rejectManualConvertedStatus enforces models.ProspectStatusConverted's own
// doc comment: that status is set automatically by Convert (which also
// creates the Lead and stamps ConvertedLeadID) and must never be settable
// directly through Create/Update — otherwise a client could flip a Prospect
// to "Converted" with no Lead ever created behind it. current is the
// pre-request status ("" for Create, which has none yet); a client
// harmlessly re-submitting an already-Converted record's unchanged status
// is allowed through, only an attempted transition into Converted from
// anything else is rejected.
func rejectManualConvertedStatus(c *fiber.Ctx, newStatus, current models.ProspectStatus) error {
	if newStatus != models.ProspectStatusConverted || current == models.ProspectStatusConverted {
		return nil
	}
	msg := `status "Converted" can only be set via POST /prospects/:id/convert`
	_ = utils.ValidationError(c, msg, map[string][]string{"status": {msg}})
	return utils.ErrHandled
}

type prospectForm struct {
	Name       string                `json:"name"`
	CompanyID  *uint                 `json:"company_id"`
	Email      string                `json:"email"`
	Phone      string                `json:"phone"`
	Source     models.ProspectSource `json:"source"`
	Status     models.ProspectStatus `json:"status"`
	Notes      string                `json:"notes"`
	AssignedTo *uint                 `json:"assigned_to"`
	// Tags is settable directly here (unlike Lead's own leadForm, which only
	// exposes tags via bulk-tag) — mirrors Contact's simpler pattern instead,
	// since Marketing editing a single Prospect's tags one at a time from its
	// detail page is a real, expected workflow.
	Tags             []string             `json:"tags"`
	BusinessUnit     *models.BusinessUnit `json:"business_unit"`
	BusinessUnitItem *string              `json:"business_unit_item"`
}

// Create godoc
// @Summary Create a prospect (Admin/Marketing/Sales Manager/Sales Rep)
// @Description Creates a new Prospect. status defaults to "New" if omitted; status "Converted" cannot be set directly (only via POST /prospects/:id/convert). source must be an active Prospect source and status an active Prospect stage. A Sales Rep may only assign to themselves.
// @Tags prospects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body prospectForm true "Prospect fields"
// @Success 201 {object} models.Prospect
// @Failure 400 {object} map[string]interface{} "Validation error (name required, invalid source/status/business_unit/email)"
// @Failure 403 {object} map[string]interface{} "Cannot assign a prospect to another team member"
// @Router /prospects [post]
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
	if !utils.IsActiveProspectSource(h.DB, string(form.Source)) {
		return utils.ValidationError(c, "source is not a valid active prospect source", map[string][]string{"source": {"invalid"}})
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}
	if err := rejectManualConvertedStatus(c, form.Status, ""); err != nil {
		return nil
	}
	if !utils.IsActiveProspectStage(h.DB, string(form.Status)) {
		return utils.ValidationError(c, "status is not a valid active prospect stage", map[string][]string{"status": {"invalid"}})
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		return utils.ValidationError(c, "business_unit must be Project or Product", map[string][]string{"business_unit": {"invalid"}})
	}

	prospect := models.Prospect{
		Name: form.Name, CompanyID: form.CompanyID, Email: form.Email, Phone: form.Phone,
		Source: form.Source, Status: form.Status, Notes: form.Notes, AssignedTo: form.AssignedTo,
		Tags:         pq.StringArray(form.Tags),
		BusinessUnit: form.BusinessUnit, BusinessUnitItem: form.BusinessUnitItem,
	}
	if prospect.Status == "" {
		prospect.Status = models.ProspectStatusNew
	}
	if err := h.DB.Create(&prospect).Error; err != nil {
		return utils.Internal(c, "Failed to create prospect")
	}
	return utils.Created(c, prospect)
}

// Get godoc
// @Summary Get a prospect by ID (Admin/Marketing/Sales Manager/Sales Rep)
// @Description Returns a single Prospect by ID.
// @Tags prospects
// @Security BearerAuth
// @Produce json
// @Param id path int true "Prospect ID"
// @Success 200 {object} models.Prospect
// @Failure 404 {object} map[string]interface{} "Prospect not found"
// @Router /prospects/{id} [get]
func (h *ProspectHandler) Get(c *fiber.Ctx) error {
	var prospect models.Prospect
	if err := h.DB.First(&prospect, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Prospect not found")
	}
	return utils.OK(c, prospect)
}

// Update godoc
// @Summary Update a prospect (Admin/Marketing/Sales Manager/Sales Rep)
// @Description Updates a Prospect, including status transitions. status "Converted" cannot be set directly (only via POST /prospects/:id/convert), except re-submitting an already-Converted record's unchanged status. source must be an active Prospect source and status an active Prospect stage. A Sales Rep may only act on/assign to their own prospects.
// @Tags prospects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Prospect ID"
// @Param body body prospectForm true "Prospect fields"
// @Success 200 {object} models.Prospect
// @Failure 400 {object} map[string]interface{} "Validation error (invalid source/status/business_unit/email)"
// @Failure 403 {object} map[string]interface{} "Not authorized to update this prospect"
// @Failure 404 {object} map[string]interface{} "Prospect not found"
// @Router /prospects/{id} [put]
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
	if !utils.IsActiveProspectSource(h.DB, string(form.Source)) {
		return utils.ValidationError(c, "source is not a valid active prospect source", map[string][]string{"source": {"invalid"}})
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}
	if err := rejectManualConvertedStatus(c, form.Status, prospect.Status); err != nil {
		return nil
	}
	if !utils.IsActiveProspectStage(h.DB, string(form.Status)) {
		return utils.ValidationError(c, "status is not a valid active prospect stage", map[string][]string{"status": {"invalid"}})
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		return utils.ValidationError(c, "business_unit must be Project or Product", map[string][]string{"business_unit": {"invalid"}})
	}

	prospect.Name, prospect.CompanyID, prospect.Email, prospect.Phone = form.Name, form.CompanyID, form.Email, form.Phone
	prospect.Source, prospect.Status, prospect.Notes, prospect.AssignedTo = form.Source, form.Status, form.Notes, form.AssignedTo
	prospect.Tags = pq.StringArray(form.Tags)
	prospect.BusinessUnit, prospect.BusinessUnitItem = form.BusinessUnit, form.BusinessUnitItem

	if err := h.DB.Save(&prospect).Error; err != nil {
		return utils.Internal(c, "Failed to update prospect")
	}
	return utils.OK(c, prospect)
}

// Delete godoc
// @Summary Delete a prospect (Admin/Marketing/Sales Manager/Sales Rep)
// @Description Soft-deletes a Prospect (AuditedModel) — recoverable via GET /prospects/trash and POST /prospects/:id/restore. A Sales Rep may only delete their own prospects.
// @Tags prospects
// @Security BearerAuth
// @Param id path int true "Prospect ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to delete this prospect"
// @Failure 404 {object} map[string]interface{} "Prospect not found"
// @Router /prospects/{id} [delete]
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

// Trash godoc
// @Summary List deleted prospects (Admin/Sales Manager)
// @Description Returns soft-deleted Prospects, paginated.
// @Tags prospects
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "Paginated prospect list (data, page, per_page, total)"
// @Router /prospects/trash [get]
func (h *ProspectHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Prospect](c, h.DB, "Failed to list deleted prospects")
}

// Restore godoc
// @Summary Restore a deleted prospect (Admin/Sales Manager)
// @Description Restores a soft-deleted Prospect.
// @Tags prospects
// @Security BearerAuth
// @Produce json
// @Param id path int true "Prospect ID"
// @Success 200 {object} models.Prospect
// @Failure 404 {object} map[string]interface{} "Deleted prospect not found"
// @Router /prospects/{id}/restore [post]
func (h *ProspectHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Prospect](c, h.DB, "Deleted prospect not found", "Failed to restore prospect")
}

// BulkReassign godoc
// @Summary Bulk reassign prospects (Admin/Sales Manager)
// @Description Reassigns a set of Prospects to a new owner in one transaction.
// @Tags prospects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkReassignForm true "Prospect IDs and new assignee"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "ids is required"
// @Router /prospects/bulk-reassign [patch]
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

// BulkTag godoc
// @Summary Bulk tag prospects (Admin/Sales Manager)
// @Description Adds or replaces tags on a set of Prospects in one transaction. mode "add" (default) merges tags, "set" replaces them.
// @Tags prospects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkTagForm true "Prospect IDs, tags, and mode"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "ids is required"
// @Router /prospects/bulk-tag [patch]
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

// BulkArchive godoc
// @Summary Bulk archive prospects (Admin/Sales Manager)
// @Description Soft-deletes a set of Prospects (same effect as Delete), in one transaction.
// @Tags prospects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkIDsForm true "Prospect IDs"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "ids is required"
// @Router /prospects/bulk-archive [patch]
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

// Convert godoc
// @Summary Convert a prospect into a lead (Admin/Marketing/Sales Manager/Sales Rep)
// @Description Converts a Prospect into a Lead (and a Company/Contact if not supplied or not already linked) in one transaction: resolve-or-create Company, resolve-or-create Contact, create the Lead with a back-reference to the source Prospect, carry over Attachments, then mark the Prospect "Converted" and stamp its converted_lead_id. Fails with 409 if already converted. Source/tags are carried over as-is even if they aren't among the Lead's own configured options.
// @Tags prospects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Prospect ID"
// @Param body body prospectConvertRequest true "Optional company_id/contact_id to link, and lead.assigned_to"
// @Success 200 {object} map[string]interface{} "lead, company, contact"
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 403 {object} map[string]interface{} "Not authorized to convert this prospect"
// @Failure 404 {object} map[string]interface{} "Prospect not found"
// @Failure 409 {object} map[string]interface{} "Prospect has already been converted"
// @Router /prospects/{id}/convert [post]
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
			// Source is carried over as-is even though ProspectSourceOption and
			// LeadSourceOption are separate lists (see ProspectSource's doc) —
			// the resulting Lead may end up with a source value ("LINE OA",
			// "Cold Outreach", ...) that isn't one of Lead's own configured
			// options. That's intentional: it preserves real information about
			// how this Lead originated rather than lossily mapping it to
			// "Other", the same way the frontend already lets a Contact's
			// role_title display a since-deactivated option instead of
			// silently blanking it.
			Name: prospect.Name, CompanyID: &company.ID, Email: prospect.Email, Phone: prospect.Phone,
			Source: models.LeadSource(prospect.Source), Status: models.LeadStatusNew, AssignedTo: req.Lead.AssignedTo,
			ProspectID: &prospect.ID,
			// Carried over as-is, same reasoning as Source above — this
			// Convert takes no per-call Deal-style payload for these (unlike
			// Lead→Deal's Convert, which lets the frontend form override
			// them), so the Prospect's own tag just passes straight through.
			BusinessUnit: prospect.BusinessUnit, BusinessUnitItem: prospect.BusinessUnitItem,
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

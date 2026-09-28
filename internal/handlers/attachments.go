package handlers

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type AttachmentHandler struct {
	DB      *gorm.DB
	Storage utils.Storage
}

func NewAttachmentHandler(db *gorm.DB, storage utils.Storage) *AttachmentHandler {
	return &AttachmentHandler{DB: db, Storage: storage}
}

// errUnknownRelatedType is attachmentParentAccess's "related_type isn't one
// of models.AttachmentRelated*" result, answered with a 422.
var errUnknownRelatedType = errors.New("unknown related_type")

// isSalesPipelineRole mirrors routes.go's salesPipelineRoles gate (the
// /deals group, and prospectRoles, which is the same four roles).
func isSalesPipelineRole(role models.Role) bool {
	switch role {
	case models.RoleAdmin, models.RoleSalesRep, models.RoleSalesManager, models.RoleMarketing:
		return true
	}
	return false
}

// attachmentParentAccess checks the record an attachment hangs off exists
// and that the caller may read it (write=false) or attach to it (write=true).
// Read access mirrors that record's own GET route: Deals/Quotes/Prospects
// are salesPipelineRoles-only, Leads/Companies/Projects are readable by any
// authenticated role. Write access adds the parent's CanWrite ownership rule
// where it has one (Deal, Quote via its Deal, Lead, Prospect) — the same
// check editing the parent itself would apply, same reasoning as
// dealForSubResource. Returns errUnknownRelatedType, errForbidden, or the
// gorm not-found error for respondAttachmentParentErr.
func attachmentParentAccess(c *fiber.Ctx, db *gorm.DB, relatedType models.AttachmentRelatedType, relatedID uint, write bool) error {
	pipelineOnly := func() error {
		if !isSalesPipelineRole(middleware.CurrentRole(c)) {
			return errForbidden
		}
		return nil
	}
	owned := func(assignedTo *uint) error {
		if write && !CanWrite(c, assignedTo) {
			return errForbidden
		}
		return nil
	}

	switch relatedType {
	case models.AttachmentRelatedDeal:
		if err := pipelineOnly(); err != nil {
			return err
		}
		var deal models.Deal
		if err := db.Select("id, assigned_to").First(&deal, relatedID).Error; err != nil {
			return err
		}
		return owned(deal.AssignedTo)
	case models.AttachmentRelatedQuote:
		if err := pipelineOnly(); err != nil {
			return err
		}
		var quote models.Quote
		if err := db.Select("id, deal_id").First(&quote, relatedID).Error; err != nil {
			return err
		}
		var deal models.Deal
		if err := db.Select("id, assigned_to").First(&deal, quote.DealID).Error; err != nil {
			return err
		}
		return owned(deal.AssignedTo)
	case models.AttachmentRelatedProspect:
		if err := pipelineOnly(); err != nil {
			return err
		}
		var prospect models.Prospect
		if err := db.Select("id, assigned_to").First(&prospect, relatedID).Error; err != nil {
			return err
		}
		return owned(prospect.AssignedTo)
	case models.AttachmentRelatedLead:
		var lead models.Lead
		if err := db.Select("id, assigned_to").First(&lead, relatedID).Error; err != nil {
			return err
		}
		return owned(lead.AssignedTo)
	case models.AttachmentRelatedCompany:
		return db.Select("id").First(&models.Company{}, relatedID).Error
	case models.AttachmentRelatedProject:
		return db.Select("id").First(&models.Project{}, relatedID).Error
	}
	return errUnknownRelatedType
}

// respondAttachmentParentErr maps attachmentParentAccess's error to a
// response: 422 unknown related_type, 403 forbidden, 404 otherwise.
func respondAttachmentParentErr(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, errUnknownRelatedType):
		return utils.ValidationError(c, "related_type is not a supported record type", map[string][]string{
			"related_type": {"must be one of lead, deal, company, project, quote, prospect"},
		})
	case errors.Is(err, errForbidden):
		return utils.Forbidden(c, "Not authorized to access this record's attachments")
	case errors.Is(err, gorm.ErrRecordNotFound):
		return utils.NotFound(c, "Related record not found")
	}
	return utils.Internal(c, "Failed to load related record")
}

// isHTTPURL reports whether raw is an absolute http:// or https:// URL —
// external_url is rendered as a link, so anything else (javascript:, data:,
// file:) is rejected rather than handed to the frontend.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

// List godoc
// @Summary List attachments for a record
// @Description Returns one record's attachments. related_type and related_id are both required, and the caller must be able to read that record (deal/quote/prospect: Admin/Sales Rep/Sales Manager/Marketing; lead/company/project: any authenticated role). Optional category filter.
// @Tags attachments
// @Security BearerAuth
// @Produce json
// @Param related_type query string true "lead, deal, company, project, quote, or prospect"
// @Param related_id query int true "Related record ID"
// @Param category query string false "Filter by category"
// @Success 200 {object} map[string]interface{} "Paginated attachment list (data, page, per_page, total)"
// @Failure 403 {object} map[string]interface{} "Caller's role cannot read this record type"
// @Failure 404 {object} map[string]interface{} "Related record not found"
// @Failure 422 {object} map[string]interface{} "related_type/related_id missing or invalid"
// @Router /attachments [get]
func (h *AttachmentHandler) List(c *fiber.Ctx) error {
	relatedType := models.AttachmentRelatedType(c.Query("related_type"))
	relatedID, parseErr := strconv.ParseUint(c.Query("related_id"), 10, 64)
	if relatedType == "" || parseErr != nil || relatedID == 0 {
		return utils.ValidationError(c, "related_type and related_id are required", map[string][]string{
			"related_type": {"required"},
			"related_id":   {"required"},
		})
	}
	if err := attachmentParentAccess(c, h.DB, relatedType, uint(relatedID), false); err != nil {
		return respondAttachmentParentErr(c, err)
	}

	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Attachment{}).
		Where("related_type = ? AND related_id = ?", relatedType, relatedID)
	if v := c.Query("category"); v != "" {
		query = query.Where("category = ?", v)
	}

	var total int64
	query.Count(&total)

	var attachments []models.Attachment
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&attachments).Error; err != nil {
		return utils.Internal(c, "Failed to list attachments")
	}
	return utils.List(c, attachments, page, perPage, total)
}

// Create godoc
// @Summary Create an attachment (Admin/Sales Rep/Sales Manager/Marketing)
// @Description Two shapes: multipart `file` upload (related_type, related_id, category form fields), or a JSON body with `external_url` (http:// or https:// only) for a linked doc. Exactly one is accepted. The related record must exist and the caller must be allowed to edit it (a Sales Rep/Marketing user only on deals, quotes, leads, and prospects assigned to them or unassigned).
// @Tags attachments
// @Security BearerAuth
// @Accept json
// @Accept multipart/form-data
// @Produce json
// @Success 201 {object} models.Attachment
// @Failure 400 {object} map[string]interface{} "Invalid body, or unsupported file type"
// @Failure 403 {object} map[string]interface{} "Not authorized to attach to this record"
// @Failure 404 {object} map[string]interface{} "Related record not found"
// @Failure 422 {object} map[string]interface{} "Missing fields, unknown related_type, or external_url not http(s)"
// @Router /attachments [post]
func (h *AttachmentHandler) Create(c *fiber.Ctx) error {
	actorID := middleware.CurrentUserID(c)

	if fh, err := c.FormFile("file"); err == nil {
		relatedType := models.AttachmentRelatedType(c.FormValue("related_type"))
		category := models.AttachmentCategory(c.FormValue("category"))
		relatedID, parseErr := strconv.ParseUint(c.FormValue("related_id"), 10, 64)
		if relatedType == "" || category == "" || parseErr != nil {
			return utils.ValidationError(c, "related_type, related_id, and category are required", map[string][]string{
				"related_type": {"required"},
				"related_id":   {"required"},
				"category":     {"required"},
			})
		}
		// Before Storage.Save, so a rejected request leaves no orphan file.
		if err := attachmentParentAccess(c, h.DB, relatedType, uint(relatedID), true); err != nil {
			return respondAttachmentParentErr(c, err)
		}

		key, size, err := h.Storage.Save(fh)
		if err != nil {
			return utils.RespondUploadError(c, err)
		}
		fileURL := "/uploads/" + key
		mimeType := fh.Header.Get("Content-Type")

		attachment := models.Attachment{
			RelatedType: relatedType, RelatedID: uint(relatedID), Category: category,
			FileName: fh.Filename, FileURL: &fileURL, FileSize: &size, MimeType: &mimeType,
			UploadedByID: actorID,
		}
		if err := h.DB.Create(&attachment).Error; err != nil {
			return utils.Internal(c, "Failed to create attachment")
		}
		return utils.Created(c, attachment)
	}

	var form struct {
		RelatedType models.AttachmentRelatedType `json:"related_type"`
		RelatedID   uint                         `json:"related_id"`
		Category    models.AttachmentCategory    `json:"category"`
		FileName    string                       `json:"file_name"`
		ExternalURL string                       `json:"external_url"`
	}
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.RelatedType == "" || form.RelatedID == 0 || form.Category == "" || form.FileName == "" || form.ExternalURL == "" {
		return utils.ValidationError(c, "related_type, related_id, category, file_name, and external_url are required", map[string][]string{
			"related_type": {"required"},
			"related_id":   {"required"},
			"category":     {"required"},
			"file_name":    {"required"},
			"external_url": {"required"},
		})
	}
	if !isHTTPURL(form.ExternalURL) {
		return utils.ValidationError(c, "external_url must be an http:// or https:// URL", map[string][]string{
			"external_url": {"must be an http:// or https:// URL"},
		})
	}
	if err := attachmentParentAccess(c, h.DB, form.RelatedType, form.RelatedID, true); err != nil {
		return respondAttachmentParentErr(c, err)
	}

	attachment := models.Attachment{
		RelatedType: form.RelatedType, RelatedID: form.RelatedID, Category: form.Category,
		FileName: form.FileName, ExternalURL: &form.ExternalURL, UploadedByID: actorID,
	}
	if err := h.DB.Create(&attachment).Error; err != nil {
		return utils.Internal(c, "Failed to create attachment")
	}
	return utils.Created(c, attachment)
}

// Delete — DELETE /attachments/:id (hard delete of the metadata row only —
// the file itself is left in object storage, no orphan-cleanup job in v1).
func (h *AttachmentHandler) Delete(c *fiber.Ctx) error {
	var attachment models.Attachment
	if err := utils.FindByID(c, h.DB, &attachment, "Attachment not found"); err != nil {
		return nil
	}
	if !CanWrite(c, &attachment.UploadedByID) {
		return utils.Forbidden(c, "Not authorized to delete this attachment")
	}
	if err := h.DB.Delete(&attachment).Error; err != nil {
		return utils.Internal(c, "Failed to delete attachment")
	}
	return utils.NoContent(c)
}

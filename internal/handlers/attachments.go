package handlers

import (
	"strconv"

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

// List — GET /attachments. Filters: related_type+related_id (required together), category.
func (h *AttachmentHandler) List(c *fiber.Ctx) error {
	relatedType := c.Query("related_type")
	relatedID := c.Query("related_id")
	if (relatedType == "") != (relatedID == "") {
		return utils.BadRequest(c, "related_type and related_id must be provided together")
	}

	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Attachment{})

	if relatedType != "" {
		query = query.Where("related_type = ? AND related_id = ?", relatedType, relatedID)
	}
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

// Create — POST /attachments (Sales/Admin, matching Project create RBAC —
// route-gated in routes.go). Two shapes: multipart `file` upload, or a JSON
// body with `external_url` for a linked doc. Exactly one is accepted.
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
	if err := h.DB.First(&attachment, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Attachment not found")
	}
	if !CanWrite(c, &attachment.UploadedByID) {
		return utils.Forbidden(c, "Not authorized to delete this attachment")
	}
	if err := h.DB.Delete(&attachment).Error; err != nil {
		return utils.Internal(c, "Failed to delete attachment")
	}
	return utils.NoContent(c)
}

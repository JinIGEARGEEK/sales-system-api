package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type TagHandler struct {
	DB *gorm.DB
}

func NewTagHandler(db *gorm.DB) *TagHandler {
	return &TagHandler{DB: db}
}

// List — GET /tags. Filters: category, status, search (name).
func (h *TagHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Tag{})

	if v := c.Query("category"); v != "" {
		query = query.Where("category = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("name ILIKE ?", "%"+v+"%")
	}

	var total int64
	query.Count(&total)

	var tags []models.Tag
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&tags).Error; err != nil {
		return utils.Internal(c, "Failed to list tags")
	}
	return utils.List(c, tags, page, perPage, total)
}

type tagForm struct {
	Name        string             `json:"name"`
	Category    models.TagCategory `json:"category"`
	Description string             `json:"description"`
	Status      models.TagStatus   `json:"status"`
}

// Create — POST /tags.
func (h *TagHandler) Create(c *fiber.Ctx) error {
	var form tagForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	tag := models.Tag{Name: form.Name, Category: form.Category, Description: form.Description, Status: form.Status}
	if tag.Status == "" {
		tag.Status = models.TagStatusActive
	}
	if err := h.DB.Create(&tag).Error; err != nil {
		return utils.ValidationError(c, "Tag name already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, tag)
}

// Update — PUT /tags/:id.
func (h *TagHandler) Update(c *fiber.Ctx) error {
	var tag models.Tag
	if err := h.DB.First(&tag, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Tag not found")
	}

	var form tagForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	tag.Name, tag.Category, tag.Description = form.Name, form.Category, form.Description
	if form.Status != "" {
		tag.Status = form.Status
	}

	if err := h.DB.Save(&tag).Error; err != nil {
		return utils.Internal(c, "Failed to update tag")
	}
	return utils.OK(c, tag)
}

// Delete — DELETE /tags/:id. Soft-delete (status: 'inactive').
func (h *TagHandler) Delete(c *fiber.Ctx) error {
	var tag models.Tag
	if err := h.DB.First(&tag, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Tag not found")
	}
	tag.Status = models.TagStatusInactive
	if err := h.DB.Save(&tag).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate tag")
	}
	return utils.NoContent(c)
}

package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// JobTitleOptionHandler — Admin CRUD for the configurable Contact job-title
// list (Contact.RoleTitle had no controlled list at all before this).
// Mirrors LeadSourceHandler's shape: List/Create/Update/Delete, Delete being
// a soft "is_active: false" flip rather than a hard row delete.
type JobTitleOptionHandler struct {
	DB *gorm.DB
}

func NewJobTitleOptionHandler(db *gorm.DB) *JobTitleOptionHandler {
	return &JobTitleOptionHandler{DB: db}
}

// List — GET /admin/job-titles. Always returns every row (active + inactive).
func (h *JobTitleOptionHandler) List(c *fiber.Ctx) error {
	var titles []models.JobTitleOption
	if err := h.DB.Order("name ASC").Find(&titles).Error; err != nil {
		return utils.Internal(c, "Failed to list job titles")
	}
	return utils.OK(c, titles)
}

type jobTitleOptionForm struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// Create — POST /admin/job-titles.
func (h *JobTitleOptionHandler) Create(c *fiber.Ctx) error {
	var form jobTitleOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	title := models.JobTitleOption{Name: form.Name, IsActive: form.IsActive == nil || *form.IsActive}
	title.CreatedBy = &actorID
	title.UpdatedBy = &actorID
	if err := h.DB.Create(&title).Error; err != nil {
		return utils.ValidationError(c, "Job title already in use", map[string][]string{"name": {"Name is already in use"}})
	}
	return utils.Created(c, title)
}

// Update — PATCH /admin/job-titles/:id.
func (h *JobTitleOptionHandler) Update(c *fiber.Ctx) error {
	var title models.JobTitleOption
	if err := h.DB.First(&title, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Job title not found")
	}

	var form jobTitleOptionForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	title.Name = form.Name
	if form.IsActive != nil {
		title.IsActive = *form.IsActive
	}
	actorID := middleware.CurrentUserID(c)
	title.UpdatedBy = &actorID

	if err := h.DB.Save(&title).Error; err != nil {
		return utils.Internal(c, "Failed to update job title")
	}
	return utils.OK(c, title)
}

// Delete — DELETE /admin/job-titles/:id. Soft-delete (is_active: false).
func (h *JobTitleOptionHandler) Delete(c *fiber.Ctx) error {
	var title models.JobTitleOption
	if err := h.DB.First(&title, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Job title not found")
	}
	title.IsActive = false
	actorID := middleware.CurrentUserID(c)
	title.DeletedBy = &actorID
	if err := h.DB.Save(&title).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate job title")
	}
	return utils.NoContent(c)
}

package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// JobTitleOptionHandler — Admin CRUD for the configurable Contact job-title
// list (Contact.RoleTitle had no controlled list at all before this).
// Mirrors LeadSourceHandler's shape: List/Create/Update/Delete, Delete being
// a soft "is_active: false" flip rather than a hard row delete. CRUD logic
// itself is OptionHandler (option_crud.go) — this just supplies the model
// type, messages, and Swagger docs.
type JobTitleOptionHandler struct {
	inner *OptionHandler[models.JobTitleOption, *models.JobTitleOption]
}

func NewJobTitleOptionHandler(db *gorm.DB) *JobTitleOptionHandler {
	return &JobTitleOptionHandler{inner: &OptionHandler[models.JobTitleOption, *models.JobTitleOption]{
		DB: db,
		Msg: OptionMessages{
			ListFail:       "Failed to list job titles",
			NotFound:       "Job title not found",
			NameConflict:   "Job title already in use",
			UpdateFail:     "Failed to update job title",
			DeactivateFail: "Failed to deactivate job title",
		},
	}}
}

// List godoc
// @Summary List job titles
// @Description Returns every configured Contact job title (active + inactive), ordered by name.
// @Tags admin/job-titles
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.JobTitleOption
// @Router /admin/job-titles [get]
func (h *JobTitleOptionHandler) List(c *fiber.Ctx) error { return h.inner.List(c) }

// Create godoc
// @Summary Create a job title
// @Description Admin-only.
// @Tags admin/job-titles
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body OptionForm true "Job title fields"
// @Success 201 {object} models.JobTitleOption
// @Failure 400 {object} map[string]interface{}
// @Router /admin/job-titles [post]
func (h *JobTitleOptionHandler) Create(c *fiber.Ctx) error { return h.inner.Create(c) }

// Update godoc
// @Summary Update a job title
// @Description Admin-only.
// @Tags admin/job-titles
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Job title ID"
// @Param body body OptionForm true "Job title fields"
// @Success 200 {object} models.JobTitleOption
// @Failure 404 {object} map[string]interface{}
// @Router /admin/job-titles/{id} [patch]
func (h *JobTitleOptionHandler) Update(c *fiber.Ctx) error { return h.inner.Update(c) }

// Delete godoc
// @Summary Deactivate a job title
// @Description Admin-only. Soft-delete (is_active: false).
// @Tags admin/job-titles
// @Security BearerAuth
// @Param id path int true "Job title ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/job-titles/{id} [delete]
func (h *JobTitleOptionHandler) Delete(c *fiber.Ctx) error { return h.inner.Delete(c) }

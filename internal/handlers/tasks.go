package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type TaskHandler struct {
	DB *gorm.DB
}

func NewTaskHandler(db *gorm.DB) *TaskHandler {
	return &TaskHandler{DB: db}
}

// List — GET /tasks. Filters: related_type+related_id (optional), status, assigned_to.
// status=pending must work without related_type/related_id for the dashboard widget.
func (h *TaskHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Task{})

	relatedType := c.Query("related_type")
	relatedID := c.Query("related_id")
	if relatedType != "" && relatedID != "" {
		query = query.Where("related_type = ? AND related_id = ?", relatedType, relatedID)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}

	var total int64
	query.Count(&total)

	var tasks []models.Task
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "due_date": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&tasks).Error; err != nil {
		return utils.Internal(c, "Failed to list tasks")
	}
	return utils.List(c, tasks, page, perPage, total)
}

type taskForm struct {
	RelatedType models.TaskRelatedType `json:"related_type"`
	RelatedID   uint                   `json:"related_id"`
	Title       string                 `json:"title"`
	DueDate     time.Time              `json:"due_date"`
	Status      models.TaskStatus      `json:"status"`
	AssignedTo  *uint                  `json:"assigned_to"`
}

// Create — POST /tasks.
func (h *TaskHandler) Create(c *fiber.Ctx) error {
	var form taskForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Title == "" {
		return utils.ValidationError(c, "title is required", map[string][]string{"title": {"required"}})
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a task to another sales rep")
	}

	task := models.Task{
		RelatedType: form.RelatedType, RelatedID: form.RelatedID, Title: form.Title,
		DueDate: form.DueDate, Status: form.Status, AssignedTo: form.AssignedTo,
	}
	if task.Status == "" {
		task.Status = models.TaskStatusPending
	}
	if err := h.DB.Create(&task).Error; err != nil {
		return utils.Internal(c, "Failed to create task")
	}
	return utils.Created(c, task)
}

// Toggle — PATCH /tasks/:id/toggle. Flips pending<->done.
func (h *TaskHandler) Toggle(c *fiber.Ctx) error {
	var task models.Task
	if err := h.DB.First(&task, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Task not found")
	}
	if !CanWrite(c, task.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this task")
	}

	if task.Status == models.TaskStatusPending {
		task.Status = models.TaskStatusDone
	} else {
		task.Status = models.TaskStatusPending
	}
	if err := h.DB.Save(&task).Error; err != nil {
		return utils.Internal(c, "Failed to toggle task")
	}
	return utils.OK(c, task)
}

// Delete — DELETE /tasks/:id (hard delete).
func (h *TaskHandler) Delete(c *fiber.Ctx) error {
	var task models.Task
	if err := h.DB.First(&task, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Task not found")
	}
	if !CanWrite(c, task.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this task")
	}
	if err := h.DB.Delete(&task).Error; err != nil {
		return utils.Internal(c, "Failed to delete task")
	}
	return utils.NoContent(c)
}

package handlers

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type TaskHandler struct {
	DB *gorm.DB
}

func NewTaskHandler(db *gorm.DB) *TaskHandler {
	return &TaskHandler{DB: db}
}

// List — GET /tasks. Filters: see applyTaskFilters (related_type[+related_id],
// status, assigned_to, campaign_id, search, business_unit, due_from,
// due_before). status=pending must work without related_type/related_id for
// the dashboard widget. Sortable by created_at (default, newest first),
// due_date or title.
func (h *TaskHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query, err := applyTaskFilters(h.DB.Model(&models.Task{}), c)
	if err != nil {
		return utils.ValidationError(c, "due_from/due_before must be an RFC 3339 timestamp or YYYY-MM-DD date", map[string][]string{"due_date": {"invalid"}})
	}

	var total int64
	query.Count(&total)

	var tasks []models.Task
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "due_date": true, "title": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&tasks).Error; err != nil {
		return utils.Internal(c, "Failed to list tasks")
	}
	return utils.List(c, tasks, page, perPage, total)
}

// errAssignTaskToOther is the 403 message for a Sales Rep assigning a task
// to someone else.
const errAssignTaskToOther = "Cannot assign a task to another sales rep"

type taskForm struct {
	RelatedType models.TaskRelatedType `json:"related_type"`
	RelatedID   uint                   `json:"related_id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	DueDate     time.Time              `json:"due_date"`
	Status      models.TaskStatus      `json:"status"`
	Priority    models.TaskPriority    `json:"priority"`
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
	if !models.IsValidTaskPriority(form.Priority) {
		return utils.ValidationError(c, "priority is invalid", map[string][]string{"priority": {"invalid"}})
	}
	if err := checkNewAssignee(c, h.DB, nil, form.AssignedTo, errAssignTaskToOther); err != nil {
		return nil
	}

	task := models.Task{
		RelatedType: form.RelatedType, RelatedID: form.RelatedID, Title: form.Title,
		Description: form.Description, DueDate: form.DueDate, Status: form.Status,
		Priority: form.Priority, AssignedTo: form.AssignedTo,
	}
	if task.Status == "" {
		task.Status = models.TaskStatusPending
	}
	if task.Priority == "" {
		task.Priority = models.TaskPriorityMedium
	}
	if err := h.DB.Create(&task).Error; err != nil {
		return utils.Internal(c, "Failed to create task")
	}
	return utils.Created(c, task)
}

type taskUpdateForm struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	DueDate     time.Time           `json:"due_date"`
	Priority    models.TaskPriority `json:"priority"`
	AssignedTo  *uint               `json:"assigned_to"`
}

// Update — PATCH /tasks/:id. Edits the fields a rep fills in on creation
// (title/description/due date/priority/assigned_to) — related_type/related_id
// stay fixed after creation (same immutability as Contract.quote_id and
// CustomerProduct.product_id), and status changes go through Toggle instead.
func (h *TaskHandler) Update(c *fiber.Ctx) error {
	var task models.Task
	if err := utils.FindByID(c, h.DB, &task, "Task not found"); err != nil {
		return nil
	}
	if !CanWrite(c, task.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this task")
	}

	var form taskUpdateForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Title == "" {
		return utils.ValidationError(c, "title is required", map[string][]string{"title": {"required"}})
	}
	if !models.IsValidTaskPriority(form.Priority) {
		return utils.ValidationError(c, "priority is invalid", map[string][]string{"priority": {"invalid"}})
	}
	if err := checkNewAssignee(c, h.DB, task.AssignedTo, form.AssignedTo, errAssignTaskToOther); err != nil {
		return nil
	}

	task.Title = form.Title
	task.Description = form.Description
	task.DueDate = form.DueDate
	task.Priority = form.Priority
	if task.Priority == "" {
		task.Priority = models.TaskPriorityMedium
	}
	task.AssignedTo = form.AssignedTo

	if err := h.DB.Save(&task).Error; err != nil {
		return utils.Internal(c, "Failed to update task")
	}
	return utils.OK(c, task)
}

// Toggle — PATCH /tasks/:id/toggle. Flips pending<->done.
func (h *TaskHandler) Toggle(c *fiber.Ctx) error {
	var task models.Task
	if err := utils.FindByID(c, h.DB, &task, "Task not found"); err != nil {
		return nil
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

type taskBulkIDsForm struct {
	IDs []uint `json:"ids"`
}

// BulkMarkDone — PATCH /tasks/bulk-mark-done. Marks every id done in one
// transaction. Open to every role (a Sales Rep clearing their own backlog is
// the main use), so ownership is checked per row with CanWrite, the rule
// Toggle uses; one forbidden row rolls back the whole call. That per-row
// check is why this and BulkReassign don't use bulk_ops.go's helpers.
func (h *TaskHandler) BulkMarkDone(c *fiber.Ctx) error {
	var form taskBulkIDsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.ValidateBulkIDCount(c, form.IDs) {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "task", "bulk_marked_done", actorID,
		func(tx *gorm.DB, task *models.Task) (models.JSONMap, models.JSONMap, error) {
			if !CanWrite(c, task.AssignedTo) {
				return nil, nil, errForbidden
			}
			before := models.JSONMap{"status": task.Status}
			task.Status = models.TaskStatusDone
			after := models.JSONMap{"status": task.Status}
			return before, after, tx.Save(task).Error
		})
	if err != nil {
		if errors.Is(err, errForbidden) {
			return utils.Forbidden(c, "Not authorized to update one or more of these tasks")
		}
		return utils.Internal(c, "Failed to bulk mark tasks done")
	}
	return utils.NoContent(c)
}

type taskBulkReassignForm struct {
	IDs        []uint `json:"ids"`
	AssignedTo *uint  `json:"assigned_to"`
}

// BulkReassign — PATCH /tasks/bulk-reassign. Checks the new assignee once
// up front (checkNewAssignee, as Create does), then CanWrite against each
// task's current assignee inside the loop, as BulkMarkDone does.
func (h *TaskHandler) BulkReassign(c *fiber.Ctx) error {
	var form taskBulkReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.ValidateBulkIDCount(c, form.IDs) {
		return nil
	}
	if err := checkNewAssignee(c, h.DB, nil, form.AssignedTo, errAssignTaskToOther); err != nil {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "task", "bulk_reassigned", actorID,
		func(tx *gorm.DB, task *models.Task) (models.JSONMap, models.JSONMap, error) {
			if !CanWrite(c, task.AssignedTo) {
				return nil, nil, errForbidden
			}
			before := models.JSONMap{"assigned_to": task.AssignedTo}
			task.AssignedTo = form.AssignedTo
			after := models.JSONMap{"assigned_to": task.AssignedTo}
			return before, after, tx.Save(task).Error
		})
	if err != nil {
		if errors.Is(err, errForbidden) {
			return utils.Forbidden(c, "Not authorized to reassign one or more of these tasks")
		}
		return utils.Internal(c, "Failed to bulk reassign tasks")
	}
	return utils.NoContent(c)
}

// Delete — DELETE /tasks/:id. A hard delete: models.Task embeds
// HardDeleteModel, as Tasks are per-rep to-dos with no Trash/Restore, unlike
// the soft-deleted Lead/Prospect/Deal/Company/Contact/User records.
func (h *TaskHandler) Delete(c *fiber.Ctx) error {
	var task models.Task
	if err := utils.FindByID(c, h.DB, &task, "Task not found"); err != nil {
		return nil
	}
	if !CanWrite(c, task.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this task")
	}
	if err := h.DB.Delete(&task).Error; err != nil {
		return utils.Internal(c, "Failed to delete task")
	}
	return utils.NoContent(c)
}

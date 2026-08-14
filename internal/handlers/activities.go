package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ActivityHandler struct {
	DB *gorm.DB
}

func NewActivityHandler(db *gorm.DB) *ActivityHandler {
	return &ActivityHandler{DB: db}
}

// List — GET /activities. Filters: related_type+related_id (required together), type.
func (h *ActivityHandler) List(c *fiber.Ctx) error {
	relatedType := c.Query("related_type")
	relatedID := c.Query("related_id")
	if (relatedType == "") != (relatedID == "") {
		return utils.BadRequest(c, "related_type and related_id must be provided together")
	}

	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Activity{})

	if relatedType != "" {
		query = query.Where("related_type = ? AND related_id = ?", relatedType, relatedID)
	}
	if v := c.Query("type"); v != "" {
		query = query.Where("type = ?", v)
	}

	var total int64
	query.Count(&total)

	var activities []models.Activity
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&activities).Error; err != nil {
		return utils.Internal(c, "Failed to list activities")
	}

	h.populateCreatedBy(activities)
	return utils.List(c, activities, page, perPage, total)
}

func (h *ActivityHandler) populateCreatedBy(activities []models.Activity) {
	ids := make(map[uint]bool)
	for _, a := range activities {
		ids[a.CreatedByID] = true
	}
	idList := make([]uint, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	var users []models.User
	h.DB.Where("id IN ?", idList).Find(&users)
	names := make(map[uint]string, len(users))
	for _, u := range users {
		names[u.ID] = u.FirstName + " " + u.LastName
	}
	for i := range activities {
		activities[i].CreatedBy = names[activities[i].CreatedByID]
	}
}

type activityForm struct {
	Type        models.ActivityType        `json:"type"`
	Subject     string                     `json:"subject"`
	Notes       string                     `json:"notes"`
	RelatedType models.ActivityRelatedType `json:"related_type"`
	RelatedID   uint                       `json:"related_id"`
}

// Create — POST /activities. Sets CreatedByID from the caller — FR-CRM-031.
func (h *ActivityHandler) Create(c *fiber.Ctx) error {
	var form activityForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.RelatedType == "" || form.RelatedID == 0 {
		return utils.ValidationError(c, "related_type and related_id are required", map[string][]string{
			"related_type": {"required"},
			"related_id":   {"required"},
		})
	}

	actorID := middleware.CurrentUserID(c)
	activity := models.Activity{
		Type: form.Type, Subject: form.Subject, Notes: form.Notes,
		RelatedType: form.RelatedType, RelatedID: form.RelatedID, CreatedByID: actorID,
	}
	if err := h.DB.Create(&activity).Error; err != nil {
		return utils.Internal(c, "Failed to create activity")
	}

	var user models.User
	if err := h.DB.First(&user, actorID).Error; err == nil {
		activity.CreatedBy = user.FirstName + " " + user.LastName
	}
	return utils.Created(c, activity)
}

// Delete — DELETE /activities/:id (hard delete).
func (h *ActivityHandler) Delete(c *fiber.Ctx) error {
	var activity models.Activity
	if err := h.DB.First(&activity, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Activity not found")
	}
	if !CanWrite(c, &activity.CreatedByID) {
		return utils.Forbidden(c, "Not authorized to delete this activity")
	}
	if err := h.DB.Delete(&activity).Error; err != nil {
		return utils.Internal(c, "Failed to delete activity")
	}
	return utils.NoContent(c)
}

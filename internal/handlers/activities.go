package handlers

import (
	"time"

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

// List — GET /activities. Filters:
//   - related_type+related_id together (a record's own timeline), or
//     related_type alone (every activity on that kind of record);
//   - type;
//   - search: subject/notes, or the linked record's display name.
//
// include_stage_changes=true additionally interleaves Deal stage-change
// history into the same paged, filtered, sorted list — see listFeed
// (activity_feed.go). Without the flag the response is the plain Activity
// list.
func (h *ActivityHandler) List(c *fiber.Ctx) error {
	relatedType := c.Query("related_type")
	relatedID := c.Query("related_id")
	if relatedID != "" && relatedType == "" {
		return utils.BadRequest(c, "related_id requires related_type")
	}

	page, perPage, offset := utils.Pagination(c)
	if c.QueryBool("include_stage_changes") && canSeeStageHistory(middleware.CurrentRole(c)) {
		return h.listFeed(c, relatedType, relatedID, page, perPage, offset)
	}

	query := applyActivityFilters(h.DB.Model(&models.Activity{}), c, relatedType, relatedID)

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
	ids := make([]uint, len(activities))
	for i, a := range activities {
		ids[i] = a.CreatedByID
	}
	names := userNamesByID(h.DB, ids)
	for i := range activities {
		activities[i].CreatedBy = names[activities[i].CreatedByID]
	}
}

// userNamesByID returns "First Last" for each distinct user id in ids, in one
// query. Unknown ids are simply absent from the map.
func userNamesByID(db *gorm.DB, ids []uint) map[uint]string {
	seen := make(map[uint]bool, len(ids))
	idList := make([]uint, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			idList = append(idList, id)
		}
	}
	names := make(map[uint]string, len(idList))
	if len(idList) == 0 {
		return names
	}
	var users []models.User
	db.Where("id IN ?", idList).Find(&users)
	for _, u := range users {
		names[u.ID] = u.FirstName + " " + u.LastName
	}
	return names
}

type activityForm struct {
	Type        models.ActivityType        `json:"type"`
	Subject     string                     `json:"subject"`
	Notes       string                     `json:"notes"`
	RelatedType models.ActivityRelatedType `json:"related_type"`
	RelatedID   uint                       `json:"related_id"`
	// CreatedAt lets a caller backdate a manually-logged Activity (e.g.
	// "mark as contacted on <past date>" from the Company page). Left nil,
	// GORM's default CreatedAt convention stamps the current time as usual.
	CreatedAt *time.Time `json:"created_at"`
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
	if form.CreatedAt != nil && form.CreatedAt.After(time.Now()) {
		return utils.ValidationError(c, "created_at cannot be in the future", map[string][]string{
			"created_at": {"invalid"},
		})
	}

	actorID := middleware.CurrentUserID(c)
	activity := models.Activity{
		Type: form.Type, Subject: form.Subject, Notes: form.Notes,
		RelatedType: form.RelatedType, RelatedID: form.RelatedID, CreatedByID: actorID,
	}
	if form.CreatedAt != nil {
		activity.CreatedAt = *form.CreatedAt
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
	if err := utils.FindByID(c, h.DB, &activity, "Activity not found"); err != nil {
		return nil
	}
	if !CanWrite(c, &activity.CreatedByID) {
		return utils.Forbidden(c, "Not authorized to delete this activity")
	}
	if err := h.DB.Delete(&activity).Error; err != nil {
		return utils.Internal(c, "Failed to delete activity")
	}
	return utils.NoContent(c)
}

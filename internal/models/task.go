package models

import "time"

type TaskStatus string

const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusDone    TaskStatus = "done"
)

// TaskPriority — a plain triage hint, no workflow behavior attached (unlike
// Status). Mirrors the LostReason enum pattern: type + const block + a
// Valid*/IsValid* pair used at the handler layer.
type TaskPriority string

const (
	TaskPriorityLow    TaskPriority = "low"
	TaskPriorityMedium TaskPriority = "medium"
	TaskPriorityHigh   TaskPriority = "high"
)

// ValidTaskPriorities lists every accepted TaskPriority value, for handler-layer validation.
var ValidTaskPriorities = []TaskPriority{TaskPriorityLow, TaskPriorityMedium, TaskPriorityHigh}

func IsValidTaskPriority(p TaskPriority) bool {
	if p == "" {
		return true
	}
	for _, v := range ValidTaskPriorities {
		if v == p {
			return true
		}
	}
	return false
}

// TaskRelatedType = ActivityRelatedType (api-system-spec.md §7.6).
type TaskRelatedType = ActivityRelatedType

// Task — api-system-spec.md §7.6.
type Task struct {
	HardDeleteModel
	// Composite index on (related_type, related_id) — every List query filters
	// on both together (see TaskHandler.List), same reasoning as Activity's.
	RelatedType TaskRelatedType `gorm:"type:varchar(16);index:idx_tasks_related,priority:1" json:"related_type"`
	RelatedID   uint            `gorm:"index:idx_tasks_related,priority:2" json:"related_id"`
	Title       string          `json:"title"`
	// Description is a free-text elaboration on Title, optional — kept as a
	// separate field rather than folded into Title so the task list can show
	// a short name with the fuller detail available on demand.
	Description string     `json:"description"`
	DueDate     time.Time  `json:"due_date"`
	Status      TaskStatus `gorm:"type:varchar(16);default:'pending';index" json:"status"`
	// Priority is a plain triage label (Low/Medium/High) — display-only, no
	// automated behavior (unlike Status), same spirit as Deal.LostReason.
	Priority TaskPriority `gorm:"type:varchar(16);default:'medium'" json:"priority"`
	// AssignedTo is a TaskHandler.List filter column — indexed for the same
	// reason Deal.AssignedTo/Lead.AssignedTo are.
	AssignedTo *uint `gorm:"index" json:"assigned_to"`
	// NotifiedAt is set once the due-date reminder email has been sent for this
	// task, so the background checker doesn't re-send it on every tick.
	NotifiedAt *time.Time `gorm:"index" json:"notified_at"`
	// CampaignID links a task to the Campaign it was bulk-created for (see
	// CampaignHandler.BulkCreateTasks) — nullable, since most tasks aren't
	// created as part of a campaign.
	CampaignID *uint `gorm:"index" json:"campaign_id"`
}

func (Task) TableName() string { return "tasks" }

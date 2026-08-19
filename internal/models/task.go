package models

import "time"

type TaskStatus string

const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusDone    TaskStatus = "done"
)

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
	DueDate     time.Time       `json:"due_date"`
	Status      TaskStatus      `gorm:"type:varchar(16);default:'pending';index" json:"status"`
	// AssignedTo is a TaskHandler.List filter column — indexed for the same
	// reason Deal.AssignedTo/Lead.AssignedTo are.
	AssignedTo *uint `gorm:"index" json:"assigned_to"`
	// NotifiedAt is set once the due-date reminder email has been sent for this
	// task, so the background checker doesn't re-send it on every tick.
	NotifiedAt *time.Time `gorm:"index" json:"notified_at"`
}

func (Task) TableName() string { return "tasks" }

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
	RelatedType TaskRelatedType `gorm:"type:varchar(16);index" json:"related_type"`
	RelatedID   uint            `gorm:"index" json:"related_id"`
	Title       string          `json:"title"`
	DueDate     time.Time       `json:"due_date"`
	Status      TaskStatus      `gorm:"type:varchar(16);default:'pending';index" json:"status"`
	AssignedTo  *uint           `json:"assigned_to"`
}

func (Task) TableName() string { return "tasks" }

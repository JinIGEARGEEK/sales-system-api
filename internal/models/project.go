package models

import "time"

type ProjectStatus string

const (
	ProjectStatusNotStarted ProjectStatus = "Not Started"
	ProjectStatusInProgress ProjectStatus = "In Progress"
	ProjectStatusOnHold     ProjectStatus = "On Hold"
	ProjectStatusCompleted  ProjectStatus = "Completed"
	ProjectStatusCancelled  ProjectStatus = "Cancelled"
)

// Project — api-system-spec.md §8.3. 🔜 Planned. A summary record only — no
// sub-resources for tasks/sprints/milestones per FR-CRM-071.
type Project struct {
	AuditedModel
	CompanyID          uint          `gorm:"not null;index" json:"company_id"`
	DealID             *uint         `gorm:"index" json:"deal_id"`
	Name               string        `gorm:"not null" json:"name"`
	Status             ProjectStatus `gorm:"type:varchar(16);default:'Not Started'" json:"status"`
	StartDate          time.Time     `json:"start_date"`
	TargetEndDate      *time.Time    `json:"target_end_date"`
	ProductionReference *string      `json:"production_reference"`
	Notes              string        `json:"notes"`
}

func (Project) TableName() string { return "projects" }

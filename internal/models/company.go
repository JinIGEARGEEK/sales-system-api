package models

import "github.com/lib/pq"

type ActiveArchivedStatus string

const (
	StatusActive   ActiveArchivedStatus = "active"
	StatusArchived ActiveArchivedStatus = "archived"
)

// Company — api-system-spec.md §4.
type Company struct {
	AuditedModel
	Name     string               `gorm:"not null" json:"name"`
	Industry string               `gorm:"index" json:"industry"`
	Size     string               `json:"size"`
	Website  string               `json:"website"`
	Tags     pq.StringArray       `gorm:"type:text[]" json:"tags"`
	Notes    string               `json:"notes"`
	Status   ActiveArchivedStatus `gorm:"type:varchar(16);default:'active';index" json:"status"`
}

func (Company) TableName() string { return "companies" }

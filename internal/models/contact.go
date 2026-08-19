package models

import "github.com/lib/pq"

// Contact — api-system-spec.md §5.
type Contact struct {
	AuditedModel
	CompanyID uint                 `gorm:"not null;index" json:"company_id"`
	Name      string               `gorm:"not null" json:"name"`
	Email     string               `json:"email"`
	Phone     string               `json:"phone"`
	RoleTitle string               `json:"role_title"`
	Tags      pq.StringArray       `gorm:"type:text[];index:idx_contacts_tags,type:gin" json:"tags"`
	Status    ActiveArchivedStatus `gorm:"type:varchar(16);default:'active'" json:"status"`
}

func (Contact) TableName() string { return "contacts" }

package models

type TagCategory string

const (
	TagCategoryTier     TagCategory = "Tier"
	TagCategoryIndustry TagCategory = "Industry"
	TagCategoryPriority TagCategory = "Priority"
)

type TagStatus string

const (
	TagStatusActive   TagStatus = "active"
	TagStatusInactive TagStatus = "inactive"
)

// Tag — api-system-spec.md §7.3.
type Tag struct {
	AuditedModel
	Name        string      `gorm:"not null;uniqueIndex" json:"name"`
	Category    TagCategory `gorm:"type:varchar(16)" json:"category"`
	Description string      `json:"description"`
	Status      TagStatus   `gorm:"type:varchar(16);default:'active'" json:"status"`
}

func (Tag) TableName() string { return "tags" }

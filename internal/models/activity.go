package models

type ActivityType string

const (
	ActivityTypeCall    ActivityType = "call"
	ActivityTypeEmail   ActivityType = "email"
	ActivityTypeMeeting ActivityType = "meeting"
)

// ActivityRelatedType is shared by Activity.related_type and Task.related_type.
type ActivityRelatedType string

const (
	RelatedTypeContact ActivityRelatedType = "contact"
	RelatedTypeCompany ActivityRelatedType = "company"
	RelatedTypeDeal    ActivityRelatedType = "deal"
)

// Activity — api-system-spec.md §7.2.
type Activity struct {
	HardDeleteModel
	Type        ActivityType        `gorm:"type:varchar(16)" json:"type"`
	Subject     string              `json:"subject"`
	Notes       string              `json:"notes"`
	RelatedType ActivityRelatedType `gorm:"type:varchar(16);index" json:"related_type"`
	RelatedID   uint                `gorm:"index" json:"related_id"`
	CreatedByID uint                `json:"-"`
	CreatedBy   string              `gorm:"-" json:"created_by"`
}

func (Activity) TableName() string { return "activities" }

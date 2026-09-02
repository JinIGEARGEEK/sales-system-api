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
	RelatedTypeContact  ActivityRelatedType = "contact"
	RelatedTypeCompany  ActivityRelatedType = "company"
	RelatedTypeDeal     ActivityRelatedType = "deal"
	RelatedTypeProspect ActivityRelatedType = "prospect"
)

// Activity — api-system-spec.md §7.2.
type Activity struct {
	HardDeleteModel
	Type    ActivityType `gorm:"type:varchar(16)" json:"type"`
	Subject string       `json:"subject"`
	Notes   string       `json:"notes"`
	// Composite index on (related_type, related_id) — every List query filters
	// on both together (see ActivityHandler.List), so this is far more
	// effective than two separate single-column indexes bitmap-AND'd together.
	RelatedType ActivityRelatedType `gorm:"type:varchar(16);index:idx_activities_related,priority:1" json:"related_type"`
	RelatedID   uint                `gorm:"index:idx_activities_related,priority:2" json:"related_id"`
	CreatedByID uint                `json:"-"`
	CreatedBy   string              `gorm:"-" json:"created_by"`
}

func (Activity) TableName() string { return "activities" }

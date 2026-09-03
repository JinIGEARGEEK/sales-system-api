package models

type NotificationEntityType string

const (
	NotificationEntityDeal     NotificationEntityType = "deal"
	NotificationEntityQuote    NotificationEntityType = "quote"
	NotificationEntityContract NotificationEntityType = "contract"
	// NotificationEntityProspect — added 2026-09-03, Marketing's own
	// staleness rule (FR-CRM-107). See NotificationRule's doc comment below.
	NotificationEntityProspect NotificationEntityType = "prospect"
)

var ValidNotificationEntityTypes = []NotificationEntityType{
	NotificationEntityDeal, NotificationEntityQuote, NotificationEntityContract, NotificationEntityProspect,
}

func IsValidNotificationEntityType(v NotificationEntityType) bool {
	for _, t := range ValidNotificationEntityTypes {
		if t == v {
			return true
		}
	}
	return false
}

type NotificationRecipientRole string

const (
	NotificationRecipientOwner            NotificationRecipientRole = "owner"
	NotificationRecipientOwnerAndManagers NotificationRecipientRole = "owner_and_managers"
)

var ValidNotificationRecipientRoles = []NotificationRecipientRole{
	NotificationRecipientOwner, NotificationRecipientOwnerAndManagers,
}

func IsValidNotificationRecipientRole(v NotificationRecipientRole) bool {
	for _, r := range ValidNotificationRecipientRoles {
		if r == v {
			return true
		}
	}
	return false
}

// NotificationRule is an Admin-configurable workflow automation rule
// (FR-CRM-100/101/102). EntityType picks which one, fixed condition applies —
// deliberately one rule shape per entity type (not a free-form condition
// expression) so a new threshold/recipient is pure config, no new backend
// hook code (FR-CRM-102), while avoiding a rule DSL this app has no other
// need for:
//
//   - "deal": an open Deal (not yet Won/Lost) sitting in its current stage
//     for at least ThresholdDays since its last stage_changed audit entry
//     (or Deal.CreatedAt if it never changed stage) — FR-CRM-100.
//   - "quote": a Sent Quote whose validity_date falls within ThresholdDays
//     from now — same definition as the Quotes Expiring Soon report
//     (FR-CRM-096) — FR-CRM-101.
//   - "contract": a Draft/Sent Contract that has sat unsigned for at least
//     ThresholdDays since creation — same definition as the Contracts Stuck
//     report (FR-CRM-097) — FR-CRM-101.
//   - "prospect": a Prospect not yet Converted/Disqualified (i.e. still
//     actively being worked) that has gone at least ThresholdDays since
//     UpdatedAt with no change — FR-CRM-107, Marketing's own funnel, added
//     2026-09-03. Unlike "deal", there's no audit-log stage-history lookup
//     (Prospect.Status changes aren't separately audited the way Deal.Stage
//     is), so UpdatedAt is the closest available "last touched" signal.
type NotificationRule struct {
	AuditedModel
	Name          string                    `gorm:"not null;uniqueIndex" json:"name"`
	EntityType    NotificationEntityType    `gorm:"type:varchar(16);not null;index" json:"entity_type"`
	ThresholdDays int                       `gorm:"not null" json:"threshold_days"`
	RecipientRole NotificationRecipientRole `gorm:"type:varchar(32);not null;default:'owner'" json:"recipient_role"`
	IsActive      bool                      `gorm:"not null;default:true;index" json:"is_active"`
}

func (NotificationRule) TableName() string { return "notification_rules" }

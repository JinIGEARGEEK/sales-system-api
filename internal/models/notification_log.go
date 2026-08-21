package models

import "time"

// NotificationLog records that NotificationRule RuleID already fired for
// entity EntityID in Context — the idempotency mechanism the background
// notifier ticker uses in place of Task's single NotifiedAt column, since one
// rule fires against many entities, and a "deal" rule can validly re-fire if
// the Deal idles again after later moving to a different stage. Context is
// the Deal's stage at the time of firing for "deal" rules, or "" for
// "quote"/"contract" rules (which only ever need to fire once per entity —
// their validity/target date doesn't change after the fact).
type NotificationLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	RuleID     uint      `gorm:"not null;uniqueIndex:idx_notification_log_unique" json:"rule_id"`
	EntityID   uint      `gorm:"not null;uniqueIndex:idx_notification_log_unique" json:"entity_id"`
	Context    string    `gorm:"not null;default:'';uniqueIndex:idx_notification_log_unique" json:"context"`
	NotifiedAt time.Time `json:"notified_at"`
}

func (NotificationLog) TableName() string { return "notification_logs" }

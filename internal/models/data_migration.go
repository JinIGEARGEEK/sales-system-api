package models

import "time"

// DataMigration records a one-time data migration (database.runOnce) as
// applied, so it never re-runs on a later boot. Deliberately minimal, like
// DocumentSequence: never shown to a user or edited directly.
type DataMigration struct {
	Name      string    `gorm:"primaryKey"`
	AppliedAt time.Time `gorm:"not null"`
}

func (DataMigration) TableName() string { return "data_migrations" }

package models

// DocumentSequence backs utils.NextDocumentNumber's race-safe per-prefix
// counter (e.g. one row per "QT202608") — see internal/utils/document_number.go
// for the atomic increment. Deliberately minimal: no audit/soft-delete
// fields, since these rows are pure counters, never shown to a user or
// edited directly.
type DocumentSequence struct {
	ID     uint   `gorm:"primaryKey"`
	Prefix string `gorm:"uniqueIndex;not null"`
	Seq    int    `gorm:"not null;default:0"`
}

func (DocumentSequence) TableName() string { return "document_sequences" }

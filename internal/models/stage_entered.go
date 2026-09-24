package models

import (
	"time"

	"gorm.io/gorm"
)

// StageEnteredAt (on Deal, Lead and Prospect) records when a record entered
// its current Stage/Status lane. It drives the Overview Pipeline page's
// "days in stage", "moved this period" and "closed this period" figures
// (FR-CRM-123). None of the three could answer that before: updated_at moves
// on any edit, and only Deal stage changes were audited.
//
// Stamped by the BeforeCreate hooks below on insert, and by each handler that
// can change a stage (Update, UpdateStatus/UpdateStage, Convert) via
// MarkStageEntered, only when the stage actually changed. MarkStageEntered
// also records PreviousStage (the lane it left); it stays nil on a record
// that has never moved. A same-lane reorder
// or an unrelated field edit leaves it alone. Rows created before this column
// existed are backfilled once by database.backfillStageEnteredAt.

func stageEnteredNow() *time.Time {
	now := time.Now()
	return &now
}

func (d *Deal) BeforeCreate(*gorm.DB) error {
	if d.StageEnteredAt == nil {
		d.StageEnteredAt = stageEnteredNow()
	}
	return nil
}

func (l *Lead) BeforeCreate(*gorm.DB) error {
	if l.StageEnteredAt == nil {
		l.StageEnteredAt = stageEnteredNow()
	}
	return nil
}

func (p *Prospect) BeforeCreate(*gorm.DB) error {
	if p.StageEnteredAt == nil {
		p.StageEnteredAt = stageEnteredNow()
	}
	return nil
}

// MarkStageEntered stamps entry into the current lane and records the lane
// it came from (PreviousStage), so the Overview Pipeline can tell a move
// forward from a slip backward.
func (d *Deal) MarkStageEntered(from string) {
	d.StageEnteredAt, d.PreviousStage = stageEnteredNow(), &from
}

func (l *Lead) MarkStageEntered(from string) {
	l.StageEnteredAt, l.PreviousStage = stageEnteredNow(), &from
}

func (p *Prospect) MarkStageEntered(from string) {
	p.StageEnteredAt, p.PreviousStage = stageEnteredNow(), &from
}

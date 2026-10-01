package models

import (
	"time"

	"gorm.io/gorm"
)

// SyncWonAt keeps WonAt in step with Status: stamped with now on the way
// into won (an already-won Deal keeps its original stamp, so re-saving it
// doesn't move its win into a later period), cleared as soon as the Deal is
// no longer won (a reopen, or a move to Lost), so a Deal won again later
// counts from its latest win.
func (d *Deal) SyncWonAt(now time.Time) {
	if d.Status != DealStatusWon {
		d.WonAt = nil
		return
	}
	if d.WonAt == nil {
		d.WonAt = &now
	}
}

// BeforeSave runs SyncWonAt on every Create/Save of a Deal struct, so every
// path that can change a Deal's status — Create, Update, UpdateStage, Lead
// conversion, the bulk helpers — maintains won_at without each handler
// having to remember to. A column-only write (Update/Updates with a map,
// UpdateColumn) doesn't persist hook changes; no handler changes status
// that way, and any that ever does must set won_at itself.
func (d *Deal) BeforeSave(*gorm.DB) error {
	d.SyncWonAt(time.Now())
	return nil
}

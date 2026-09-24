package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

const maxStaleDays = 365

// staleDaysFromBody reads the optional "stale_days" field of a
// PipelineStage/ProspectStage create or update body. The stage forms are
// otherwise applied as full replacements, but stale_days is newer than
// every existing client, so an update that doesn't send it must leave the
// saved threshold alone rather than clearing it: present reports whether
// the key was in the body at all. value is nil for an explicit null (use
// models.DefaultStaleDays). fields is set when the value is invalid.
func staleDaysFromBody(c *fiber.Ctx) (value *int, present bool, fields map[string][]string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &raw); err != nil {
		return nil, false, nil
	}
	v, ok := raw["stale_days"]
	if !ok {
		return nil, false, nil
	}
	if string(v) == "null" {
		return nil, true, nil
	}
	var n int
	if err := json.Unmarshal(v, &n); err != nil || n < 1 || n > maxStaleDays {
		return nil, true, map[string][]string{"stale_days": {"must be a whole number of days from 1 to 365, or null for the default"}}
	}
	return &n, true, nil
}

// renameStageReferences repoints every record (soft-deleted ones included)
// from a renamed stage to its new name, in both its current-stage column and
// previous_stage, inside the rename's own transaction. Records store their
// stage as the name, so without this a rename would drop them out of every
// board lane and fail the active-stage check the next time they're saved.
// UpdateColumn so no record's updated_at moves — a rename isn't an edit to
// the record. No-op when the name didn't change.
func renameStageReferences(tx *gorm.DB, model interface{}, column, oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	if err := tx.Model(model).Unscoped().Where(column+" = ?", oldName).UpdateColumn(column, newName).Error; err != nil {
		return err
	}
	return tx.Model(model).Unscoped().Where("previous_stage = ?", oldName).UpdateColumn("previous_stage", newName).Error
}

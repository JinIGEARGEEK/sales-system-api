package handlers

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

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

// Records store their stage by name, in fixed-width columns: deals.stage is
// varchar(64), prospects.status varchar(16). A longer stage name would fail
// the rename cascade (and every later save onto that stage).
const (
	maxPipelineStageNameLen = 64
	maxProspectStageNameLen = 16
)

// stageNameFields validates a (trimmed) stage name for create and update:
// required, and short enough for the records' column.
func stageNameFields(name string, maxLen int) (map[string][]string, string) {
	if name == "" {
		return map[string][]string{"name": {"required"}}, "name is required"
	}
	if utf8.RuneCountInString(name) > maxLen {
		msg := fmt.Sprintf("must be at most %d characters", maxLen)
		return map[string][]string{"name": {msg}}, "name " + msg
	}
	return nil, ""
}

// stageNameTaken reports whether a stage other than excludeID (0 on create)
// already has name — checked up front so a clash is a 422, not the unique
// index's 500. Unscoped: the index covers soft-deleted rows too.
func stageNameTaken(db *gorm.DB, model interface{}, name string, excludeID uint) (bool, error) {
	var count int64
	err := db.Model(model).Unscoped().Where("name = ? AND id <> ?", name, excludeID).Count(&count).Error
	return count > 0, err
}

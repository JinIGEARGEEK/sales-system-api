package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
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

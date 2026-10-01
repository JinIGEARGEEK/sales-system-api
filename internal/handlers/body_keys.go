package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
)

// bodyFields is a JSON request body's top-level fields, raw. BodyParser
// can't tell an omitted field from an explicit null or zero value; handlers
// with partial-merge semantics (keep what's omitted, clear what's null)
// check the key here.
type bodyFields map[string]json.RawMessage

// has reports whether the body contained key at the top level.
func (b bodyFields) has(key string) bool {
	_, ok := b[key]
	return ok
}

// bodyKeys parses the body's top-level fields. ok is false when the body
// isn't a JSON object (BodyParser also accepts form encoding); callers that
// require JSON answer 400 then, others treat it as "no keys sent".
func bodyKeys(c *fiber.Ctx) (fields bodyFields, ok bool) {
	if err := json.Unmarshal(c.Body(), &fields); err != nil {
		return nil, false
	}
	return fields, true
}

// bodyHas reports whether the JSON body has key at the top level (false for
// a non-JSON body).
func bodyHas(c *fiber.Ctx, key string) bool {
	fields, _ := bodyKeys(c)
	return fields.has(key)
}

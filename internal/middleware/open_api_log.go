package middleware

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	fiberutils "github.com/gofiber/fiber/v2/utils"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// LogOpenAPIWrites records every mutating call an /open/* API key makes into
// models.OpenAPIRequestLog — see that model's doc for why this exists
// alongside AuditLogEntry. Read-only calls (GET) aren't logged; they don't
// change data, and logging every list/get would dwarf the write log with no
// real benefit. Must run after RequireAPIKey (needs CurrentAPIKeyID) — and,
// for the replay check below, after RequireIdempotency too (it must be the
// group-level middleware wrapping that route-level one, so this resumes
// after RequireIdempotency has already decided whether it replayed).
//
// Excludes GET rather than allowlisting {POST, PUT} (as this used to, before
// Project/Product's Open API Update added PATCH to the mix): every /open/*
// route is one of GET/POST/PUT/PATCH, so "not GET" is the correct, and only,
// generalization that doesn't need a third method name added by hand the
// next time a new resource picks yet another write verb — POST/PUT/PATCH all
// mutate and all deserve an audit-log entry.
//
// Best-effort: the DB write happens in a goroutine after the response is
// already on the wire, so a logging failure (or a slow one) never delays or
// breaks the caller's actual request.
func LogOpenAPIWrites(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		method := c.Method()
		if method == fiber.MethodGet {
			return c.Next()
		}

		err := c.Next()

		if replayed, _ := c.Locals(LocalIdempotentReplay).(bool); replayed {
			// This response is Idempotency-Key replaying a prior attempt's
			// result verbatim, not a new write — that attempt was already
			// logged once; logging it again would show a single logical
			// operation as two separate writes in this key's history.
			return err
		}

		keyID, _ := CurrentAPIKeyID(c)
		ownerID := CurrentUserID(c)
		// Method/Path are zero-copy strings backed by fasthttp's request
		// buffer (see fiber.Ctx.Method/Path) — that buffer gets reused for
		// the next request on this connection as soon as this handler
		// returns, so it must be copied before it can be read from the
		// goroutine below, which outlives the request. Everything else on
		// entry is already a plain value (ints, or strings derived from
		// switch/strconv/json.Unmarshal, none of which alias the buffer).
		entry := models.OpenAPIRequestLog{
			APIKeyID:     keyID,
			OwnerUserID:  ownerID,
			Method:       fiberutils.CopyString(method),
			Path:         fiberutils.CopyString(c.Path()),
			ResourceType: openAPIResourceType(c.Path()),
			ResourceID:   openAPIResourceID(c),
			StatusCode:   c.Response().StatusCode(),
		}
		utils.SafeGo(func() { db.Create(&entry) })

		return err
	}
}

func openAPIResourceType(path string) string {
	switch {
	case strings.Contains(path, "/companies"):
		return "company"
	case strings.Contains(path, "/contacts"):
		return "contact"
	case strings.Contains(path, "/projects"):
		return "project"
	case strings.Contains(path, "/products"):
		return "product"
	case strings.Contains(path, "/prospects"):
		return "prospect"
	case strings.Contains(path, "/leads"):
		return "lead"
	default:
		return ""
	}
}

// openAPIResourceID resolves the affected row's ID: the :id path param on an
// Update, or the newly-created row's id from the Create response body
// otherwise. Returns 0 if neither is available (e.g. a failed Create).
func openAPIResourceID(c *fiber.Ctx) uint {
	if idStr := c.Params("id"); idStr != "" {
		if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
			return uint(id)
		}
	}
	var parsed struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(c.Response().Body(), &parsed); err == nil {
		return parsed.Data.ID
	}
	return 0
}

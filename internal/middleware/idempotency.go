package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// IdempotencyKeyHeader is the header an Open API caller sends to make a
// POST safe to retry.
const IdempotencyKeyHeader = "Idempotency-Key"

// idempotencyKeyWindow bounds how long a key stays live for replay/conflict
// checking — generous enough to cover any realistic client retry loop
// without treating a value reused months later as still "the same request".
// There's no garbage collector for rows older than this yet (nice-to-have,
// doesn't affect correctness — see the `created_at` bound in every lookup
// below); it just stops old rows from being treated as live.
const idempotencyKeyWindow = 24 * time.Hour

// LocalIdempotentReplay marks c.Locals when RequireIdempotency served a
// cached response instead of running the handler. LogOpenAPIWrites
// (open_api_log.go) checks this so a client's retry — the exact case
// idempotency exists to make a no-op — doesn't show up as a second,
// indistinguishable write in that key's request-log history.
const LocalIdempotentReplay = "idempotency_replayed"

// RequireIdempotency makes POST /open/companies and /open/contacts safe to
// retry: a client that resends the same Idempotency-Key with the same body
// gets back the exact response the first attempt produced, instead of
// risking a second Company/Contact being created because the first
// attempt's response was lost to a timeout/network blip. A key reused with a
// DIFFERENT body is rejected outright (409) as a caller bug, rather than
// silently honoring whichever request happened to be stored first.
//
// Absent header = opt-out: existing integrations that don't send one behave
// exactly as before this existed. Must run after RequireAPIKey — it needs
// CurrentAPIKeyID to scope the key per API key, not globally.
//
// Concurrency: the very first thing this does is INSERT a placeholder row
// keyed on (api_key_id, key) — the DB's unique index is what actually
// prevents two simultaneous retries from both reaching the handler and both
// creating a row; the in-memory checks below only handle the cases the
// insert alone can't (mismatched body, still-in-flight, already-completed).
func RequireIdempotency(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := c.Get(IdempotencyKeyHeader)
		if key == "" {
			return c.Next()
		}
		apiKeyID, ok := CurrentAPIKeyID(c)
		if !ok {
			// Shouldn't happen behind RequireAPIKey — fail open rather than
			// crash; the request just proceeds without idempotency.
			return c.Next()
		}
		sum := sha256.Sum256(c.Body())
		bodyHash := hex.EncodeToString(sum[:])
		cutoff := time.Now().Add(-idempotencyKeyWindow)

		claim := models.IdempotencyKey{
			APIKeyID: apiKeyID, Key: key, Method: c.Method(), Path: c.Path(),
			RequestHash: bodyHash,
		}
		if err := db.Create(&claim).Error; err != nil {
			// Most likely the unique index rejecting a repeat (api_key_id, key)
			// — look up what's already there rather than assuming which case
			// this is.
			var existing models.IdempotencyKey
			loadErr := db.Where("api_key_id = ? AND key = ? AND created_at >= ?", apiKeyID, key, cutoff).
				First(&existing).Error
			if loadErr != nil {
				return utils.Internal(c, "Failed to process Idempotency-Key")
			}
			if existing.RequestHash != bodyHash {
				return utils.Conflict(c, "Idempotency-Key was already used with a different request body")
			}
			if existing.ResponseStatus == 0 {
				return utils.Conflict(c, "A request with this Idempotency-Key is already in progress")
			}
			c.Locals(LocalIdempotentReplay, true)
			c.Status(existing.ResponseStatus)
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			return c.Send(existing.ResponseBody)
		}

		if err := c.Next(); err != nil {
			// The handler errored before producing a real response — release
			// the claim so a corrected retry isn't blocked by this failed one.
			db.Where("id = ?", claim.ID).Delete(&models.IdempotencyKey{})
			return err
		}

		status := c.Response().StatusCode()
		if status >= 200 && status < 300 {
			body := append([]byte(nil), c.Response().Body()...)
			db.Model(&models.IdempotencyKey{}).Where("id = ?", claim.ID).
				Updates(map[string]interface{}{"response_status": status, "response_body": body})
		} else {
			// Don't cache a failed attempt (a 422 from a typo the caller then
			// fixes, say) — release the key instead of permanently wedging it
			// to that one failure.
			db.Where("id = ?", claim.ID).Delete(&models.IdempotencyKey{})
		}
		return nil
	}
}

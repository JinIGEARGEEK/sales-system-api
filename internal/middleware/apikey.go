package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// apiKeyCacheTTL mirrors authCacheTTL's tradeoff (authcache.go) — a revoked
// key or deactivated owner keeps working for up to this long on instances
// that already cached it, in exchange for not hitting Postgres on every
// external call.
const apiKeyCacheTTL = 30 * time.Second

// apiKeyState is the per-key data RequireAPIKey needs, keyed by KeyHash so a
// cache hit never needs the DB at all.
type apiKeyState struct {
	keyID       uint
	ownerUserID uint
	ownerRole   models.Role
	valid       bool // key active AND owner active
	expiresAt   time.Time
}

var apiKeyCache sync.Map // map[string]apiKeyState, keyed by KeyHash

func apiKeyCacheGet(hash string) (apiKeyState, bool) {
	v, ok := apiKeyCache.Load(hash)
	if !ok {
		return apiKeyState{}, false
	}
	entry := v.(apiKeyState)
	if time.Now().After(entry.expiresAt) {
		return apiKeyState{}, false
	}
	return entry, true
}

func apiKeyCacheSet(hash string, state apiKeyState) {
	state.expiresAt = time.Now().Add(apiKeyCacheTTL)
	apiKeyCache.Store(hash, state)
}

// InvalidateAPIKeyCache drops any cached state for a key — call wherever a
// key is revoked/deleted so the next call re-reads the DB instead of a stale
// "still valid" cached entry (same reasoning as InvalidateAuthCache).
func InvalidateAPIKeyCache(hash string) {
	apiKeyCache.Delete(hash)
}

// resetAPIKeyCacheForTests mirrors resetAuthCacheForTests — see ResetForTests
// (pwcache.go) for why the integration suite needs this between tests.
func resetAPIKeyCacheForTests() {
	apiKeyCache.Range(func(key, _ interface{}) bool {
		apiKeyCache.Delete(key)
		return true
	})
}

const apiKeyHeader = "X-API-Key"

// RequireAPIKey authenticates the /open/* integration routes (routes.Setup)
// against models.APIKey instead of the staff Bearer-JWT flow RequireAuth
// enforces — a server-to-server caller has no user session to log in as.
//
// A valid key sets the same c.Locals RequireAuth would (LocalUserID,
// LocalRole), pinned to the key's OwnerUserID/that user's current Role, so
// the reused CompanyHandler/ContactHandler code downstream (created_by/
// updated_by, any future RequireRoles gate) behaves exactly as it would for
// a normal logged-in request from that staff member.
func RequireAPIKey(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw := c.Get(apiKeyHeader)
		if raw == "" {
			return utils.Unauthorized(c, "Missing "+apiKeyHeader+" header")
		}
		hash := utils.HashAPIKey(raw)

		state, cached := apiKeyCacheGet(hash)
		if !cached {
			var row struct {
				ID          uint
				OwnerUserID uint
				IsActive    bool
				OwnerActive bool
				OwnerRole   models.Role
			}
			err := db.Table("api_keys").
				Select("api_keys.id, api_keys.owner_user_id, api_keys.is_active, users.is_active as owner_active, users.role as owner_role").
				Joins("JOIN users ON users.id = api_keys.owner_user_id").
				Where("api_keys.key_hash = ?", hash).
				Take(&row).Error
			if err != nil {
				return utils.Unauthorized(c, "Invalid API key")
			}
			state = apiKeyState{
				keyID:       row.ID,
				ownerUserID: row.OwnerUserID,
				ownerRole:   row.OwnerRole,
				valid:       row.IsActive && row.OwnerActive,
			}
			apiKeyCacheSet(hash, state)

			// Stamp last_used_at only on the DB round trip we're already
			// making (a cache miss) rather than on every request — an
			// integration can easily call this hundreds of times a minute, so
			// writing on every single one would be a write for every read
			// with no real benefit: last_used_at is an operational "is this
			// key still in use" signal for the Admin key list, not an
			// exact-to-the-request audit field, so being accurate to within
			// apiKeyCacheTTL is enough. Best-effort: an error here shouldn't
			// fail (or measurably slow down) the caller's actual request.
			if state.valid {
				go db.Model(&models.APIKey{}).Where("id = ?", row.ID).Update("last_used_at", time.Now())
			}
		}
		if !state.valid {
			return utils.Unauthorized(c, "Invalid API key")
		}

		c.Locals(LocalUserID, state.ownerUserID)
		c.Locals(LocalRole, state.ownerRole)
		return c.Next()
	}
}

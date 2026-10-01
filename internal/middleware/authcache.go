package middleware

import (
	"sync"
	"time"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// authCacheTTL bounds how stale a cached authState can get across instances
// (e.g. a multi-replica deploy where a deactivation/logout lands on a
// different instance than a subsequent request). Same-instance reads are
// exact — Invalidate is called synchronously wherever the underlying row
// changes — this TTL is only a cross-instance safety net, same tradeoff as
// mustChangeCache above.
const authCacheTTL = 30 * time.Second

// authState is the per-user data RequireAuth needs to decide whether an
// otherwise-valid JWT should still be honored: an account deactivated after
// the token was issued, or a token issued before the holder's most recent
// logout/forced-logout (see TokenVersion on models.User). role is the
// account's current role, which RequireAuth trusts over the JWT's own role
// claim so a demotion takes effect without waiting for the token to expire.
type authState struct {
	isActive     bool
	tokenVersion int
	role         models.Role
	expiresAt    time.Time
}

// authCache avoids a `SELECT is_active, token_version, role` round trip on every
// single authenticated request. Keyed by plain userID — see mustChangeCache's
// doc for why that's fine in production but needs ResetForTests in the
// integration suite (RESTART IDENTITY reuses IDs across tests).
var authCache sync.Map // map[uint]authState

func authCacheGet(userID uint) (authState, bool) {
	v, ok := authCache.Load(userID)
	if !ok {
		return authState{}, false
	}
	entry := v.(authState)
	if time.Now().After(entry.expiresAt) {
		return authState{}, false
	}
	return entry, true
}

func authCacheSet(userID uint, state authState) {
	state.expiresAt = time.Now().Add(authCacheTTL)
	authCache.Store(userID, state)
}

// InvalidateAuthCache drops any cached auth state for a user — call this
// wherever is_active, token_version or role is written (deactivation,
// deletion, logout, role change, password change/reset) so the next request re-reads the DB instead of a
// stale cached value. Safe to call even if nothing was ever cached.
func InvalidateAuthCache(userID uint) {
	authCache.Delete(userID)
}

// resetAuthCacheForTests mirrors pwcache's ResetForTests — see its doc.
func resetAuthCacheForTests() {
	authCache.Range(func(key, _ interface{}) bool {
		authCache.Delete(key)
		return true
	})
}

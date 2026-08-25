package middleware

import (
	"sync"
	"time"
)

// mustChangeCacheTTL bounds how stale a cached must_change_password flag can
// get across instances (e.g. a multi-replica deploy where the password-change
// request lands on a different instance than a subsequent read). Same-instance
// reads are exact — see Invalidate below — this TTL is only a cross-instance
// safety net.
const mustChangeCacheTTL = 30 * time.Second

type mustChangeCacheEntry struct {
	value     bool
	expiresAt time.Time
}

// mustChangeCache avoids hitting the database on every single authenticated
// request just to check one boolean (RequirePasswordChanged used to run a
// `Pluck` per request). Invalidate is called synchronously wherever the flag
// changes so the common single-instance case never serves a stale value.
//
// Keyed by plain userID — fine for production, where user IDs are never
// reused. The integration test suite truncates its tables with RESTART
// IDENTITY between tests, though, which *does* reuse IDs across otherwise
// unrelated tests sharing one process-lifetime cache — see ResetForTests.
var mustChangeCache sync.Map // map[uint]mustChangeCacheEntry

func mustChangeCacheGet(userID uint) (bool, bool) {
	v, ok := mustChangeCache.Load(userID)
	if !ok {
		return false, false
	}
	entry := v.(mustChangeCacheEntry)
	if time.Now().After(entry.expiresAt) {
		return false, false
	}
	return entry.value, true
}

func mustChangeCacheSet(userID uint, value bool) {
	mustChangeCache.Store(userID, mustChangeCacheEntry{value: value, expiresAt: time.Now().Add(mustChangeCacheTTL)})
}

// InvalidateMustChangePassword drops any cached must_change_password value for
// a user — call this wherever the flag is written (password change, admin
// reset, account creation) so the next request re-reads the DB instead of a
// stale cached value.
func InvalidateMustChangePassword(userID uint) {
	mustChangeCache.Delete(userID)
}

// ResetForTests clears the entire cache. The integration suite's shared test
// DB gets TRUNCATE ... RESTART IDENTITY'd between tests (see
// testutil.TruncateAll), which reuses primary keys across tests — without
// this, a must_change_password value cached for e.g. userID 1 in one test
// would leak into an unrelated later test whose own userID-1 user has a
// different value. Not for production use.
func ResetForTests() {
	mustChangeCache.Range(func(key, _ interface{}) bool {
		mustChangeCache.Delete(key)
		return true
	})
	resetAuthCacheForTests()
}

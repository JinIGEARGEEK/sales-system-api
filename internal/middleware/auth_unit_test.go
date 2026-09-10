package middleware

// Package-internal unit tests for the pieces of this package that need no
// database at all: role gating, the request-locals accessors, and the
// authCache/mustChangeCache TTL + invalidate logic. Deliberately package
// `middleware` (not `middleware_test`) so it can reach the unexported
// authCache/mustChangeCache internals directly — these can't import
// internal/testutil anyway (see auth_db_test.go for the DB-backed cases,
// which live in an external package for exactly that reason).

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// newHTTPRequest is a tiny helper so each case below doesn't repeat the
// httptest.NewRequest boilerplate.
func newHTTPRequest(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func TestRequireRoles_AllowedRolePassesThrough(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c *fiber.Ctx) error {
		c.Locals(LocalRole, models.RoleAdmin)
		return c.Next()
	}, RequireRoles(models.RoleAdmin, models.RoleSalesManager), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := newHTTPRequest("GET", "/x")
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
}

func TestRequireRoles_DisallowedRoleGets403(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c *fiber.Ctx) error {
		c.Locals(LocalRole, models.RoleSalesRep)
		return c.Next()
	}, RequireRoles(models.RoleAdmin, models.RoleSalesManager), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := newHTTPRequest("GET", "/x")
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

func TestRequireRoles_NoRoleSetGets403NotUnauthenticated(t *testing.T) {
	// RequireRoles runs after RequireAuth in the real middleware chain, so an
	// unset role local (zero value) must still be a 403 (authenticated but
	// not authorized), never a panic or a 401 — RequireRoles itself never
	// decides authentication, only authorization.
	app := fiber.New()
	app.Get("/x", RequireRoles(models.RoleAdmin), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := newHTTPRequest("GET", "/x")
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

func TestIsManager(t *testing.T) {
	cases := []struct {
		role models.Role
		want bool
	}{
		{models.RoleAdmin, true},
		{models.RoleSalesManager, true},
		{models.RoleSalesRep, false},
		{models.Role("Bogus"), false},
		{models.Role(""), false},
	}
	for _, tc := range cases {
		app := fiber.New()
		var got bool
		app.Get("/x", func(c *fiber.Ctx) error {
			c.Locals(LocalRole, tc.role)
			got = IsManager(c)
			return c.SendStatus(fiber.StatusOK)
		})
		req := newHTTPRequest("GET", "/x")
		_, err := app.Test(req)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "role %q", tc.role)
	}
}

func TestCurrentUserIDAndCurrentRole(t *testing.T) {
	app := fiber.New()
	var gotID uint
	var gotRole models.Role
	app.Get("/x", func(c *fiber.Ctx) error {
		c.Locals(LocalUserID, uint(42))
		c.Locals(LocalRole, models.RoleSalesManager)
		gotID = CurrentUserID(c)
		gotRole = CurrentRole(c)
		return c.SendStatus(fiber.StatusOK)
	})
	req := newHTTPRequest("GET", "/x")
	_, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, uint(42), gotID)
	require.Equal(t, models.RoleSalesManager, gotRole)
}

func TestCurrentUserIDAndCurrentRole_ZeroValueWhenUnset(t *testing.T) {
	app := fiber.New()
	var gotID uint
	var gotRole models.Role
	gotID = 99 // pre-seed with a non-zero sentinel to prove it gets overwritten
	app.Get("/x", func(c *fiber.Ctx) error {
		gotID = CurrentUserID(c)
		gotRole = CurrentRole(c)
		return c.SendStatus(fiber.StatusOK)
	})
	req := newHTTPRequest("GET", "/x")
	_, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, uint(0), gotID)
	require.Equal(t, models.Role(""), gotRole)
}

// --- authCache / mustChangeCache TTL + invalidate logic ---

func TestAuthCache_SetGetAndInvalidate(t *testing.T) {
	defer ResetForTests()
	ResetForTests()

	const uid = uint(1001)
	_, ok := authCacheGet(uid)
	require.False(t, ok, "miss before anything is cached")

	authCacheSet(uid, true, 3)
	state, ok := authCacheGet(uid)
	require.True(t, ok)
	require.True(t, state.isActive)
	require.Equal(t, 3, state.tokenVersion)

	InvalidateAuthCache(uid)
	_, ok = authCacheGet(uid)
	require.False(t, ok, "invalidate must force the next read to miss")
}

func TestAuthCache_ExpiresAfterTTL(t *testing.T) {
	defer ResetForTests()
	ResetForTests()

	const uid = uint(1002)
	authCache.Store(uid, authState{
		isActive:     true,
		tokenVersion: 1,
		expiresAt:    time.Now().Add(-time.Second), // already expired
	})
	_, ok := authCacheGet(uid)
	require.False(t, ok, "an expired entry must read back as a miss")
}

func TestMustChangeCache_SetGetAndInvalidate(t *testing.T) {
	defer ResetForTests()
	ResetForTests()

	const uid = uint(2001)
	_, ok := mustChangeCacheGet(uid)
	require.False(t, ok)

	mustChangeCacheSet(uid, true)
	val, ok := mustChangeCacheGet(uid)
	require.True(t, ok)
	require.True(t, val)

	InvalidateMustChangePassword(uid)
	_, ok = mustChangeCacheGet(uid)
	require.False(t, ok)
}

func TestMustChangeCache_ExpiresAfterTTL(t *testing.T) {
	defer ResetForTests()
	ResetForTests()

	const uid = uint(2002)
	mustChangeCache.Store(uid, mustChangeCacheEntry{
		value:     true,
		expiresAt: time.Now().Add(-time.Second),
	})
	_, ok := mustChangeCacheGet(uid)
	require.False(t, ok, "an expired entry must read back as a miss")
}

func TestResetForTests_ClearsBothCaches(t *testing.T) {
	authCacheSet(3001, true, 0)
	mustChangeCacheSet(3002, true)

	ResetForTests()

	_, ok := authCacheGet(3001)
	require.False(t, ok)
	_, ok = mustChangeCacheGet(3002)
	require.False(t, ok)
}

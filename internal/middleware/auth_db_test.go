// External test package (middleware_test, not middleware) so it can import
// both internal/middleware and internal/testutil without an import cycle —
// internal/testutil itself imports internal/middleware (and handlers/routes),
// so a `package middleware` internal test file could never import testutil.
//
// These cover RequireAuth/RequirePasswordChanged's DB-lookup + cache
// interaction end to end (through a real route, GET /api/v1/auth/me, which
// sits behind exactly RequireAuth + RequirePasswordChanged) rather than
// re-testing the pure-logic pieces already covered by auth_unit_test.go.
package middleware_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

func TestRequireAuth_ActiveUserWithMatchingTokenVersionPasses(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/auth/me", nil, user.ID, user.Role)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequireAuth_DeactivatedUsersTokenIsRejected(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)

	// Mint the token while still active, then deactivate — mirrors the real
	// scenario this guards against: a JWT issued before an Admin deactivates
	// the account must stop working immediately, not just after it expires.
	token := testutil.Token(t, user.ID, user.Role)
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", user.ID).Update("is_active", false).Error)
	middleware.InvalidateAuthCache(user.ID) // same call the real deactivate path makes

	req := testutil.NewRequest(t, http.MethodGet, "/api/v1/auth/me", nil, token)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRequireAuth_StaleTokenVersionIsRejected(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)

	// Token minted for version 0 (testutil.Token's convention); bump the DB's
	// TokenVersion (as Logout/forced-logout would) without invalidating the
	// cache explicitly — RequireAuth must still catch the mismatch, whether
	// via a fresh DB read or a cache entry, since token_version is part of
	// what's compared against on every request.
	token := testutil.Token(t, user.ID, user.Role)
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", user.ID).Update("token_version", 1).Error)
	middleware.InvalidateAuthCache(user.ID)

	req := testutil.NewRequest(t, http.MethodGet, "/api/v1/auth/me", nil, token)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRequirePasswordChanged_BlocksNonExemptRouteUntilChanged(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", user.ID).Update("must_change_password", true).Error)

	// /auth/me is one of the exempt paths — must still succeed.
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/auth/me", nil, user.ID, user.Role)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "auth/me is exempt from the must-change-password gate")

	// Any other authenticated route must be blocked with 403 until the
	// password is changed.
	req2 := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals", nil, user.ID, user.Role)
	resp2, err := app.Test(req2)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, resp2.StatusCode)
}

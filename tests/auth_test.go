package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

func TestLogin_Success(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email":    user.Email,
		"password": testutil.TestPassword,
	}, "")

	var body struct {
		Data struct {
			AccessToken string      `json:"access_token"`
			User        models.User `json:"user"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body.Data.AccessToken)
	assert.Equal(t, user.ID, body.Data.User.ID)
}

func TestLogin_WrongPassword(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email":    user.Email,
		"password": "not-the-password",
	}, "")

	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestLogin_UnknownUser(t *testing.T) {
	app, _ := testutil.App(t)

	req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email":    "does-not-exist@igeargeek.com",
		"password": "whatever",
	}, "")

	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMe_WithValidToken(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/auth/me", nil, user.ID, user.Role)
	var body struct {
		Data models.User `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, user.ID, body.Data.ID)
}

func TestMe_WithoutToken(t *testing.T) {
	app, _ := testutil.App(t)

	req := testutil.NewRequest(t, http.MethodGet, "/api/v1/auth/me", nil, "")
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestChangePassword_Success(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)
	require.NoError(t, db.Model(user).Update("must_change_password", true).Error)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/auth/change-password", map[string]string{
		"current_password": testutil.TestPassword,
		"new_password":     "a-new-password1",
		"confirm_password": "a-new-password1",
	}, user.ID, user.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.User
	require.NoError(t, db.First(&reloaded, user.ID).Error)
	assert.False(t, reloaded.MustChangePassword)

	loginReq := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email":    user.Email,
		"password": "a-new-password1",
	}, "")
	loginResp := doJSON(t, app, loginReq, nil)
	assert.Equal(t, http.StatusOK, loginResp.StatusCode, "must be able to log in with the new password")
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/auth/change-password", map[string]string{
		"current_password": "not-the-password",
		"new_password":     "a-new-password1",
		"confirm_password": "a-new-password1",
	}, user.ID, user.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestChangePassword_ConfirmMismatch(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/auth/change-password", map[string]string{
		"current_password": testutil.TestPassword,
		"new_password":     "a-new-password1",
		"confirm_password": "does-not-match",
	}, user.ID, user.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestRequirePasswordChanged_BlocksOtherRoutes proves the forced-change gate is
// enforced server-side: an account still on an Admin-assigned password can
// reach only /auth/me, /auth/logout, and /auth/change-password — every other
// authenticated route (here, /team-members) is blocked until it changes.
func TestRequirePasswordChanged_BlocksOtherRoutes(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)
	require.NoError(t, db.Model(user).Update("must_change_password", true).Error)

	blocked := testutil.AuthRequest(t, http.MethodGet, "/api/v1/team-members", nil, user.ID, user.Role)
	blockedResp := doJSON(t, app, blocked, nil)
	assert.Equal(t, http.StatusForbidden, blockedResp.StatusCode)

	me := testutil.AuthRequest(t, http.MethodGet, "/api/v1/auth/me", nil, user.ID, user.Role)
	meResp := doJSON(t, app, me, nil)
	assert.Equal(t, http.StatusOK, meResp.StatusCode, "/auth/me must stay reachable so the frontend can detect the forced-change state")
}

// TestMe_RejectsNonHS256Alg proves the JWT algorithm-pinning fix: a token
// forged with HS384 using the *same* secret bytes must still be rejected,
// because utils.ParseToken pins jwt.WithValidMethods to HS256 only.
func TestMe_RejectsNonHS256Alg(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)
	cfg := testutil.Config()

	claims := jwt.MapClaims{
		"user_id": user.ID,
		"role":    string(user.Role),
		"exp":     jwt.NewNumericDate(time.Now().Add(time.Hour)).Unix(),
		"iat":     jwt.NewNumericDate(time.Now()).Unix(),
	}
	forged := jwt.NewWithClaims(jwt.SigningMethodHS384, claims)
	signed, err := forged.SignedString([]byte(cfg.JWTSecret))
	require.NoError(t, err)

	req := testutil.NewRequest(t, http.MethodGet, "/api/v1/auth/me", nil, signed)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "HS384-signed token with the right secret bytes must still be rejected")
}

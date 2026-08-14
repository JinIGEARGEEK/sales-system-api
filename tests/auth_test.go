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
		"username": user.Username,
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
		"username": user.Username,
		"password": "not-the-password",
	}, "")

	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestLogin_UnknownUser(t *testing.T) {
	app, _ := testutil.App(t)

	req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "does-not-exist",
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

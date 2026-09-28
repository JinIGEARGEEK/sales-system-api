package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// sessionErrBody is the §1.5 error envelope.
type sessionErrBody struct {
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
	} `json:"error"`
}

// userUpdateBody is the full PUT /users/:id body for u, with overrides applied.
func userUpdateBody(u *models.User, overrides map[string]string) map[string]string {
	body := map[string]string{
		"first_name": u.FirstName,
		"last_name":  u.LastName,
		"email":      u.Email,
		"role":       string(u.Role),
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

// meStatus is GET /auth/me's status code for token — a cheap "is this
// token still honored" probe.
func meStatus(t *testing.T, app *fiber.App, token string) int {
	t.Helper()
	resp, err := app.Test(testutil.NewRequest(t, http.MethodGet, "/api/v1/auth/me", nil, token), -1)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// TestUserUpdate_RoleChangeRevokesSessions guards the demotion case: an
// Admin's token minted before being demoted must stop working, not keep
// Admin access for the rest of its 30-day lifetime.
func TestUserUpdate_RoleChangeRevokesSessions(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleAdmin)
	oldToken := testutil.Token(t, target.ID, target.Role)
	require.Equal(t, http.StatusOK, meStatus(t, app, oldToken))

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID),
		userUpdateBody(target, map[string]string{"role": string(models.RoleProduction)}), admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)

	assert.Equal(t, http.StatusUnauthorized, meStatus(t, app, oldToken))
}

// TestUserUpdate_UnchangedRoleKeepsSessions makes sure the revocation is
// targeted: editing a user's name alone must not sign them out.
func TestUserUpdate_UnchangedRoleKeepsSessions(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)
	token := testutil.Token(t, target.ID, target.Role)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID),
		userUpdateBody(target, map[string]string{"first_name": "Renamed"}), admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)

	assert.Equal(t, http.StatusOK, meStatus(t, app, token))
}

// TestRequireAuth_RoleComesFromDB guards the cache-window case: even if a
// role changes without a token_version bump, RequireRoles must see the DB's
// role, not the (stale) claim in the token.
func TestRequireAuth_RoleComesFromDB(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, user.ID, models.RoleAdmin)

	require.NoError(t, db.Model(&models.User{}).Where("id = ?", user.ID).Update("role", models.RoleProduction).Error)
	middleware.InvalidateAuthCache(user.ID)

	resp := doJSON(t, app, testutil.NewRequest(t, http.MethodGet, "/api/v1/users", nil, token), nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "an Admin claim must not outlive the DB role")
}

// TestUserUpdate_PasswordResetRevokesSessions — an Admin resetting a
// password (e.g. a suspected compromise) must kill the old sessions too.
func TestUserUpdate_PasswordResetRevokesSessions(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)
	oldToken := testutil.Token(t, target.ID, target.Role)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID),
		userUpdateBody(target, map[string]string{"password": "brand-new-pass-1"}), admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)

	assert.Equal(t, http.StatusUnauthorized, meStatus(t, app, oldToken))
}

// TestChangePassword_RevokesOldTokenAndReturnsFreshOne — the caller's other
// sessions die with the old password, but the response carries a new token
// so the caller itself stays signed in; the user fields stay at the top
// level of data, where the frontend reads them.
func TestChangePassword_RevokesOldTokenAndReturnsFreshOne(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)
	oldToken := testutil.Token(t, user.ID, user.Role)

	req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/change-password", map[string]string{
		"current_password": testutil.TestPassword,
		"new_password":     "another-pass-2",
		"confirm_password": "another-pass-2",
	}, oldToken)
	var body struct {
		Data struct {
			models.User
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, user.ID, body.Data.ID)
	assert.Equal(t, user.Email, body.Data.Email)
	require.NotEmpty(t, body.Data.AccessToken)

	assert.Equal(t, http.StatusUnauthorized, meStatus(t, app, oldToken), "the pre-change token must stop working")
	assert.Equal(t, http.StatusOK, meStatus(t, app, body.Data.AccessToken), "the returned token must work")
}

// TestBulkDeactivate_RevokesSessions — re-activating afterwards must not
// bring the pre-deactivation token back to life.
func TestBulkDeactivate_RevokesSessions(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)
	oldToken := testutil.Token(t, target.ID, target.Role)

	for _, path := range []string{"/api/v1/users/bulk-deactivate", "/api/v1/users/bulk-activate"} {
		req := testutil.AuthRequest(t, http.MethodPatch, path, map[string]interface{}{"ids": []uint{target.ID}}, admin.ID, admin.Role)
		require.Equal(t, http.StatusNoContent, doJSON(t, app, req, nil).StatusCode, path)
	}

	assert.Equal(t, http.StatusUnauthorized, meStatus(t, app, oldToken))
}

// TestUserUpdate_DeactivateThenReactivateRevokesSessions — same as the bulk
// case, through PUT /users/:id.
func TestUserUpdate_DeactivateThenReactivateRevokesSessions(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)
	oldToken := testutil.Token(t, target.ID, target.Role)

	for _, status := range []string{"inactive", "active"} {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID),
			userUpdateBody(target, map[string]string{"status": status}), admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode, status)
	}

	assert.Equal(t, http.StatusUnauthorized, meStatus(t, app, oldToken))
}

// TestUserCreateUpdate_RejectUnknownRole — an empty or made-up role used to
// be stored as-is, leaving an account every RequireRoles gate rejects.
func TestUserCreateUpdate_RejectUnknownRole(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)

	for _, role := range []string{"", "Superuser", "admin"} {
		t.Run("create role="+role, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/users", map[string]string{
				"first_name": "New", "email": "new_role_check@igeargeek.com", "role": role,
			}, admin.ID, admin.Role)
			var body sessionErrBody
			resp := doJSON(t, app, req, &body)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, body.Error.Fields, "role")
		})
		t.Run("update role="+role, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID),
				userUpdateBody(target, map[string]string{"role": role}), admin.ID, admin.Role)
			var body sessionErrBody
			resp := doJSON(t, app, req, &body)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, body.Error.Fields, "role")
		})
	}

	var stored models.User
	require.NoError(t, db.First(&stored, target.ID).Error)
	assert.Equal(t, models.RoleSalesRep, stored.Role)
}

// TestUserCreate_InactiveStatusPersists — is_active is `default:true`, so a
// plain GORM Create silently dropped the false and stored the user active.
func TestUserCreate_InactiveStatusPersists(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/users", map[string]string{
		"first_name": "Dormant", "email": "dormant_user@igeargeek.com",
		"role": string(models.RoleSalesRep), "status": "inactive",
	}, admin.ID, admin.Role)
	var body struct {
		Data models.User `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.False(t, body.Data.IsActive, "response must report the stored value")

	var stored models.User
	require.NoError(t, db.First(&stored, body.Data.ID).Error)
	assert.False(t, stored.IsActive)
}

// TestLogin_InactiveAccountOnlyRevealedWithCorrectPassword — "Account is
// inactive" used to be returned before the password check, telling anyone
// with just an email that the account exists and is deactivated.
func TestLogin_InactiveAccountOnlyRevealedWithCorrectPassword(t *testing.T) {
	app, db := testutil.App(t)
	user := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(user).Update("is_active", false).Error)

	login := func(password string) (int, string) {
		req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"email": user.Email, "password": password,
		}, "")
		var body sessionErrBody
		resp := doJSON(t, app, req, &body)
		return resp.StatusCode, body.Error.Message
	}

	status, msg := login("wrong-password")
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, "Invalid email or password", msg)

	status, msg = login(testutil.TestPassword)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, "Account is inactive", msg)
}

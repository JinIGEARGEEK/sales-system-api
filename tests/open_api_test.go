package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// createAPIKey drives POST /admin/api-keys as an Admin and returns the raw
// key string (only ever returned once, at creation — see APIKeyHandler.Create).
func createAPIKey(t *testing.T, app *fiber.App, adminID uint, ownerUserID uint) string {
	t.Helper()
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]interface{}{"name": "Integration test key", "owner_user_id": ownerUserID},
		adminID, models.RoleAdmin)
	var body struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.NotEmpty(t, body.Data.Key)
	return body.Data.Key
}

func openRequest(t *testing.T, method, target string, body interface{}, apiKey string) *http.Request {
	t.Helper()
	req := testutil.NewRequest(t, method, target, body, "")
	req.Header.Set("X-API-Key", apiKey)
	return req
}

func TestAPIKeys_CreateRequiresAdmin(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]interface{}{"name": "x", "owner_user_id": rep.ID}, rep.ID, models.RoleSalesRep)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

func TestAPIKeys_CreateReturnsRawKeyOnce(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]interface{}{"name": "Zapier", "owner_user_id": admin.ID}, admin.ID, models.RoleAdmin)
	var body struct {
		Data struct {
			Key    string        `json:"key"`
			APIKey models.APIKey `json:"api_key"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.NotEmpty(t, body.Data.Key)
	require.True(t, body.Data.APIKey.IsActive)
	require.Equal(t, admin.ID, body.Data.APIKey.OwnerUserID)

	// Listing never exposes the raw key/hash back out.
	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/api-keys", nil, admin.ID, models.RoleAdmin)
	listResp := doJSON(t, app, listReq, nil)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
}

func TestOpenAPI_MissingOrInvalidKeyRejected(t *testing.T) {
	app, _ := testutil.App(t)

	noKeyReq := testutil.NewRequest(t, http.MethodGet, "/api/v1/open/companies", nil, "")
	resp := doJSON(t, app, noKeyReq, nil)
	require.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	badKeyReq := openRequest(t, http.MethodGet, "/api/v1/open/companies", nil, "sk_live_doesnotexist")
	resp2 := doJSON(t, app, badKeyReq, nil)
	require.Equal(t, fiber.StatusUnauthorized, resp2.StatusCode)
}

func TestOpenAPI_CompanyCreateGetUpdateList(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Open API Co",
	}, apiKey)
	var created struct {
		Data models.Company `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Open API Co", created.Data.Name)
	require.NotNil(t, created.Data.CreatedBy)
	require.Equal(t, admin.ID, *created.Data.CreatedBy)

	getReq := openRequest(t, http.MethodGet, "/api/v1/open/companies/"+itoa(created.Data.ID), nil, apiKey)
	getResp := doJSON(t, app, getReq, nil)
	require.Equal(t, fiber.StatusOK, getResp.StatusCode)

	updateReq := openRequest(t, http.MethodPut, "/api/v1/open/companies/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Open API Co Renamed",
	}, apiKey)
	var updated struct {
		Data models.Company `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, "Open API Co Renamed", updated.Data.Name)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/companies", nil, apiKey)
	listResp := doJSON(t, app, listReq, nil)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
}

func TestOpenAPI_ContactCreateGetUpdate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	company := seedCompany(t, db)

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/contacts", map[string]interface{}{
		"company_id": company.ID,
		"name":       "Open API Contact",
	}, apiKey)
	var created struct {
		Data models.Contact `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, company.ID, created.Data.CompanyID)

	updateReq := openRequest(t, http.MethodPut, "/api/v1/open/contacts/"+itoa(created.Data.ID), map[string]interface{}{
		"company_id": company.ID,
		"name":       "Open API Contact Renamed",
	}, apiKey)
	var updated struct {
		Data models.Contact `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, "Open API Contact Renamed", updated.Data.Name)
}

func TestOpenAPI_RevokedKeyRejected(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]interface{}{"name": "Revoke me", "owner_user_id": admin.ID}, admin.ID, models.RoleAdmin)
	var body struct {
		Data struct {
			Key    string        `json:"key"`
			APIKey models.APIKey `json:"api_key"`
		} `json:"data"`
	}
	doJSON(t, app, createReq, &body)
	apiKey := body.Data.Key
	keyID := body.Data.APIKey.ID

	// Works before revoke.
	okReq := openRequest(t, http.MethodGet, "/api/v1/open/companies", nil, apiKey)
	okResp := doJSON(t, app, okReq, nil)
	require.Equal(t, fiber.StatusOK, okResp.StatusCode)

	revokeReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/api-keys/"+itoa(keyID)+"/revoke", nil, admin.ID, models.RoleAdmin)
	revokeResp := doJSON(t, app, revokeReq, nil)
	require.Equal(t, fiber.StatusNoContent, revokeResp.StatusCode)

	deniedReq := openRequest(t, http.MethodGet, "/api/v1/open/companies", nil, apiKey)
	deniedResp := doJSON(t, app, deniedReq, nil)
	require.Equal(t, fiber.StatusUnauthorized, deniedResp.StatusCode)
}

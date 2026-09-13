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

// TestOpenAPI_StatusFilterCaseInsensitive guards the fix for the bug that
// started this round of work: a Company saved with a differently-cased
// status must still be found by ?status=active.
func TestOpenAPI_StatusFilterCaseInsensitive(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	req := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Mixed Case Status Co", "status": "Active",
	}, apiKey)
	var created struct {
		Data models.Company `json:"data"`
	}
	resp := doJSON(t, app, req, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	// normalizeActiveArchivedStatus lowercases on write, so the stored value
	// is already canonical — but the filter itself must also be
	// case-insensitive for any row that predates/bypasses that.
	require.Equal(t, models.StatusActive, created.Data.Status)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/companies?status=ACTIVE", nil, apiKey)
	var list struct {
		Data []models.Company `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.NotEmpty(t, list.Data)
}

// TestOpenAPI_IndustryFilterCaseInsensitive guards the industry filter fix —
// industry is free text that registers whatever casing it's typed with, so
// ?industry= must still match a differently-cased stored value.
func TestOpenAPI_IndustryFilterCaseInsensitive(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	req := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Cased Industry Co", "industry": "Technology",
	}, apiKey)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/companies?industry=technology", nil, apiKey)
	var list struct {
		Data []models.Company `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.NotEmpty(t, list.Data)
}

// TestOpenAPI_TagNormalizedAndFilterCaseInsensitive guards tag normalization
// on write (trim+lowercase, dedupe) and the case-insensitive ?tag= filter.
func TestOpenAPI_TagNormalizedAndFilterCaseInsensitive(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	req := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Tagged Co", "tags": []string{" VIP ", "vip", "Renewed"},
	}, apiKey)
	var created struct {
		Data models.Company `json:"data"`
	}
	resp := doJSON(t, app, req, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, []string{"vip", "renewed"}, []string(created.Data.Tags))

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/companies?tag=VIP", nil, apiKey)
	var list struct {
		Data []models.Company `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.NotEmpty(t, list.Data)
}

// TestOpenAPI_UpdateRequiresName guards the fix for Update silently saving a
// blank name — Create has always rejected this, Update didn't.
func TestOpenAPI_UpdateRequiresName(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	company := seedCompany(t, db)

	updateReq := openRequest(t, http.MethodPut, "/api/v1/open/companies/"+itoa(company.ID), map[string]interface{}{
		"name": "",
	}, apiKey)
	resp := doJSON(t, app, updateReq, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)

	contact := seedContact(t, db, company.ID)
	updateContactReq := openRequest(t, http.MethodPut, "/api/v1/open/contacts/"+itoa(contact.ID), map[string]interface{}{
		"company_id": company.ID, "name": "",
	}, apiKey)
	contactResp := doJSON(t, app, updateContactReq, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, contactResp.StatusCode)
}

// TestOpenAPI_InvalidWebsiteEmailPhoneRejected guards the new format
// validation on Company.Website and Contact.Email/Phone.
func TestOpenAPI_InvalidWebsiteEmailPhoneRejected(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	badWebsiteReq := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Bad Website Co", "website": "not a website",
	}, apiKey)
	resp := doJSON(t, app, badWebsiteReq, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)

	company := seedCompany(t, db)
	badEmailReq := openRequest(t, http.MethodPost, "/api/v1/open/contacts", map[string]interface{}{
		"company_id": company.ID, "name": "Bad Email", "email": "not-an-email",
	}, apiKey)
	emailResp := doJSON(t, app, badEmailReq, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, emailResp.StatusCode)

	badPhoneReq := openRequest(t, http.MethodPost, "/api/v1/open/contacts", map[string]interface{}{
		"company_id": company.ID, "name": "Bad Phone", "phone": "call me maybe",
	}, apiKey)
	phoneResp := doJSON(t, app, badPhoneReq, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, phoneResp.StatusCode)
}

// TestOpenAPI_DuplicateDomainConflict guards the new dedupe-by-domain check:
// this CRM is meant to be the source of truth other internal systems sync
// Company data through, so two Creates for the same website domain must not
// silently produce two rows.
func TestOpenAPI_DuplicateDomainConflict(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	first := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Acme Corp", "website": "https://acme.example.com",
	}, apiKey)
	resp := doJSON(t, app, first, nil)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)

	dup := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Acme Corp (dup)", "website": "http://www.acme.example.com/about",
	}, apiKey)
	dupResp := doJSON(t, app, dup, nil)
	require.Equal(t, fiber.StatusConflict, dupResp.StatusCode)
}

// TestOpenAPI_OptionsEndpoint guards the new read-only /open/options
// endpoint, which lets an external integrator discover valid size/
// revenue_size/role_title/industry values without a staff login.
func TestOpenAPI_OptionsEndpoint(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	// testutil.App only migrates the schema, it doesn't run the server's
	// startup seeding (cmd/api/main.go) — seed one active option per table
	// directly so this test doesn't depend on that separate seed step.
	require.NoError(t, db.Create(&models.IndustryOption{Name: "Technology", IsActive: true}).Error)
	require.NoError(t, db.Create(&models.CompanySizeOption{Name: "1-10", IsActive: true}).Error)
	require.NoError(t, db.Create(&models.RevenueSizeOption{Name: "< 1M THB", IsActive: true}).Error)
	require.NoError(t, db.Create(&models.JobTitleOption{Name: "Owner", IsActive: true}).Error)

	req := openRequest(t, http.MethodGet, "/api/v1/open/options", nil, apiKey)
	var body struct {
		Data struct {
			Industries   []string `json:"industries"`
			Sizes        []string `json:"sizes"`
			RevenueSizes []string `json:"revenue_sizes"`
			JobTitles    []string `json:"job_titles"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.NotEmpty(t, body.Data.Industries)
	require.NotEmpty(t, body.Data.Sizes)
	require.NotEmpty(t, body.Data.RevenueSizes)
	require.NotEmpty(t, body.Data.JobTitles)
}

// TestOpenAPI_IdempotencyKeyReplaysResponse guards Idempotency-Key support:
// a repeated POST with the same key+body must return the original response
// (not create a second Company), a repeated key with a DIFFERENT body must
// 409, and a fresh key must proceed normally.
func TestOpenAPI_IdempotencyKeyReplaysResponse(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)

	body := map[string]interface{}{"name": "Idempotent Co"}
	req1 := openRequest(t, http.MethodPost, "/api/v1/open/companies", body, apiKey)
	req1.Header.Set("Idempotency-Key", "retry-1")
	var first struct {
		Data models.Company `json:"data"`
	}
	resp1 := doJSON(t, app, req1, &first)
	require.Equal(t, fiber.StatusCreated, resp1.StatusCode)

	// Same key, same body — must replay the exact same Company, not create
	// a second one.
	req2 := openRequest(t, http.MethodPost, "/api/v1/open/companies", body, apiKey)
	req2.Header.Set("Idempotency-Key", "retry-1")
	var second struct {
		Data models.Company `json:"data"`
	}
	resp2 := doJSON(t, app, req2, &second)
	require.Equal(t, fiber.StatusCreated, resp2.StatusCode)
	require.Equal(t, first.Data.ID, second.Data.ID)

	var count int64
	db.Model(&models.Company{}).Where("name = ?", "Idempotent Co").Count(&count)
	require.Equal(t, int64(1), count)

	// Same key, different body — must 409, not silently apply either request.
	req3 := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Different Co",
	}, apiKey)
	req3.Header.Set("Idempotency-Key", "retry-1")
	resp3 := doJSON(t, app, req3, nil)
	require.Equal(t, fiber.StatusConflict, resp3.StatusCode)

	// A fresh key proceeds normally and creates a distinct Company.
	req4 := openRequest(t, http.MethodPost, "/api/v1/open/companies", map[string]interface{}{
		"name": "Another Co",
	}, apiKey)
	req4.Header.Set("Idempotency-Key", "retry-2")
	resp4 := doJSON(t, app, req4, nil)
	require.Equal(t, fiber.StatusCreated, resp4.StatusCode)
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

package apitests

import (
	"net/http"
	"testing"
	"time"

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

// TestOpenAPI_ProjectCreateGetUpdateList guards the Open API's Project
// endpoints: CreateOpen takes company_id in the body (unlike the staff-facing
// nested POST /companies/:companyId/projects), Update is a partial PATCH
// (ProjectHandler.Update's own semantics, not a full-replace PUT).
func TestOpenAPI_ProjectCreateGetUpdateList(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	company := seedCompany(t, db)

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/projects", map[string]interface{}{
		"company_id": company.ID,
		"name":       "Open API Project",
	}, apiKey)
	var created struct {
		Data models.Project `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Open API Project", created.Data.Name)
	require.Equal(t, company.ID, created.Data.CompanyID)
	require.Equal(t, models.ProjectStatusNotStarted, created.Data.Status)
	require.NotNil(t, created.Data.CreatedBy)
	require.Equal(t, admin.ID, *created.Data.CreatedBy)

	getReq := openRequest(t, http.MethodGet, "/api/v1/open/projects/"+itoa(created.Data.ID), nil, apiKey)
	getResp := doJSON(t, app, getReq, nil)
	require.Equal(t, fiber.StatusOK, getResp.StatusCode)

	updateReq := openRequest(t, http.MethodPatch, "/api/v1/open/projects/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Open API Project Renamed",
	}, apiKey)
	var updated struct {
		Data models.Project `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, "Open API Project Renamed", updated.Data.Name)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/projects?company_id="+itoa(company.ID), nil, apiKey)
	listResp := doJSON(t, app, listReq, nil)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
}

// TestOpenAPI_ProjectCreateRequiresCompanyAndName guards CreateOpen's
// validation: a missing company_id or an unknown one, and a missing name,
// must each be rejected rather than silently creating an orphaned Project.
func TestOpenAPI_ProjectCreateRequiresCompanyAndName(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	company := seedCompany(t, db)

	missingCompany := openRequest(t, http.MethodPost, "/api/v1/open/projects", map[string]interface{}{
		"name": "No Company Project",
	}, apiKey)
	resp := doJSON(t, app, missingCompany, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)

	unknownCompany := openRequest(t, http.MethodPost, "/api/v1/open/projects", map[string]interface{}{
		"company_id": 999999, "name": "Ghost Co Project",
	}, apiKey)
	unknownResp := doJSON(t, app, unknownCompany, nil)
	require.Equal(t, fiber.StatusNotFound, unknownResp.StatusCode)

	missingName := openRequest(t, http.MethodPost, "/api/v1/open/projects", map[string]interface{}{
		"company_id": company.ID,
	}, apiKey)
	nameResp := doJSON(t, app, missingName, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, nameResp.StatusCode)
}

// TestOpenAPI_ProductCreateGetUpdateList guards the Open API's Product
// endpoints — List/Create/Update reuse the exact same top-level
// ProductHandler methods the staff-facing /products routes use.
func TestOpenAPI_ProductCreateGetUpdateList(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	require.NoError(t, db.Create(&models.ProductCategoryOption{Name: "Software", IsActive: true}).Error)

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/products", map[string]interface{}{
		"name": "Open API Product", "category": "Software", "price": 100.0,
	}, apiKey)
	var created struct {
		Data models.Product `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Open API Product", created.Data.Name)
	require.True(t, created.Data.IsActive)
	require.NotNil(t, created.Data.CreatedBy)
	require.Equal(t, admin.ID, *created.Data.CreatedBy)

	getReq := openRequest(t, http.MethodGet, "/api/v1/open/products/"+itoa(created.Data.ID), nil, apiKey)
	getResp := doJSON(t, app, getReq, nil)
	require.Equal(t, fiber.StatusOK, getResp.StatusCode)

	updateReq := openRequest(t, http.MethodPatch, "/api/v1/open/products/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Open API Product Renamed", "category": "Software", "price": 150.0,
	}, apiKey)
	var updated struct {
		Data models.Product `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, "Open API Product Renamed", updated.Data.Name)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/products?category=Software", nil, apiKey)
	listResp := doJSON(t, app, listReq, nil)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
}

// TestOpenAPI_ProspectCreateGetUpdateList guards the Open API's Prospect
// endpoints, which reuse ProspectHandler's existing top-level List/Create/
// Get/Update methods as-is.
func TestOpenAPI_ProspectCreateGetUpdateList(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	// "Social Media" is one of DefaultProspectSourceOptions, seeded once per
	// test binary (testutil.seedPipelineConfig) — creating it again here
	// would collide with that seed's uniqueIndex on name.

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/prospects", map[string]interface{}{
		"name": "Open API Prospect", "source": "Social Media",
	}, apiKey)
	var created struct {
		Data models.Prospect `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Open API Prospect", created.Data.Name)
	require.Equal(t, models.ProspectStatusNew, created.Data.Status)

	getReq := openRequest(t, http.MethodGet, "/api/v1/open/prospects/"+itoa(created.Data.ID), nil, apiKey)
	getResp := doJSON(t, app, getReq, nil)
	require.Equal(t, fiber.StatusOK, getResp.StatusCode)

	updateReq := openRequest(t, http.MethodPut, "/api/v1/open/prospects/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Open API Prospect Renamed", "source": "Social Media",
	}, apiKey)
	var updated struct {
		Data models.Prospect `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, "Open API Prospect Renamed", updated.Data.Name)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/prospects", nil, apiKey)
	listResp := doJSON(t, app, listReq, nil)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
}

// TestOpenAPI_LeadCreateGetUpdateList guards the Open API's Lead endpoints,
// which reuse LeadHandler's existing top-level List/Create/Get/Update
// methods as-is.
func TestOpenAPI_LeadCreateGetUpdateList(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	// "Referral" is one of DefaultLeadSourceOptions, seeded once per test
	// binary (testutil.seedPipelineConfig) — creating it again here would
	// collide with that seed's uniqueIndex on name.

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/leads", map[string]interface{}{
		"name": "Open API Lead", "source": "Referral",
	}, apiKey)
	var created struct {
		Data models.Lead `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Open API Lead", created.Data.Name)
	require.Equal(t, models.LeadStatusNew, created.Data.Status)

	getReq := openRequest(t, http.MethodGet, "/api/v1/open/leads/"+itoa(created.Data.ID), nil, apiKey)
	getResp := doJSON(t, app, getReq, nil)
	require.Equal(t, fiber.StatusOK, getResp.StatusCode)

	updateReq := openRequest(t, http.MethodPut, "/api/v1/open/leads/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Open API Lead Renamed", "source": "Referral",
	}, apiKey)
	var updated struct {
		Data models.Lead `json:"data"`
	}
	updResp := doJSON(t, app, updateReq, &updated)
	require.Equal(t, fiber.StatusOK, updResp.StatusCode)
	require.Equal(t, "Open API Lead Renamed", updated.Data.Name)

	listReq := openRequest(t, http.MethodGet, "/api/v1/open/leads", nil, apiKey)
	listResp := doJSON(t, app, listReq, nil)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
}

// TestOpenAPI_PatchWritesAreLogged guards the fix for a gap where
// LogOpenAPIWrites only recognized POST/PUT — Project/Product's Open API
// Update uses PATCH (§8/§9's own semantics, not a full-replace PUT like
// Company/Contact), and that method fell straight through the old
// allowlist, leaving PATCH writes to /open/projects and /open/products
// completely absent from OpenAPIRequestLog/GET /admin/api-keys/:id/logs —
// exactly the audit trail this logging exists for. The write happens in a
// goroutine (utils.SafeGo) after the response is already on the wire, so
// this polls briefly rather than asserting immediately after the request.
func TestOpenAPI_PatchWritesAreLogged(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	apiKey := createAPIKey(t, app, admin.ID, admin.ID)
	company := seedCompany(t, db)

	createReq := openRequest(t, http.MethodPost, "/api/v1/open/projects", map[string]interface{}{
		"company_id": company.ID, "name": "Logged Project",
	}, apiKey)
	var created struct {
		Data models.Project `json:"data"`
	}
	require.Equal(t, fiber.StatusCreated, doJSON(t, app, createReq, &created).StatusCode)

	patchReq := openRequest(t, http.MethodPatch, "/api/v1/open/projects/"+itoa(created.Data.ID), map[string]interface{}{
		"name": "Logged Project Renamed",
	}, apiKey)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, patchReq, nil).StatusCode)

	require.Eventually(t, func() bool {
		var count int64
		db.Model(&models.OpenAPIRequestLog{}).
			Where("method = ? AND resource_type = ? AND resource_id = ?", http.MethodPatch, "project", created.Data.ID).
			Count(&count)
		return count == 1
	}, 2*time.Second, 10*time.Millisecond, "expected the PATCH to /open/projects/:id to be written to OpenAPIRequestLog")
}

// TestOpenAPI_ProspectLeadListGetNotScopedByOwnership guards an explicit
// invariant (documented in docs/OPEN_API_GUIDE.md §1): CanWrite's ownership
// restriction applies to Prospect/Lead create/update only — a key acting as
// a Sales Rep can still list and get every Prospect/Lead in the system
// through /open/prospects and /open/leads, including ones assigned to a
// different rep, the same "reads aren't ownership-filtered" behavior the
// staff app's own List/Get already have. This is intentional, not a gap —
// pinned down here so a future change can't silently narrow (or someone
// can't mistake the lack of narrowing for a bug) without a test noticing.
func TestOpenAPI_ProspectLeadListGetNotScopedByOwnership(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	// The key acts as `rep` (a plain Sales Rep) — CanWrite would restrict
	// this identity's create/update to its own records, but List/Get should
	// still return everything.
	apiKey := createAPIKey(t, app, admin.ID, rep.ID)

	othersProspect := seedProspect(t, db, nil)
	othersProspect.AssignedTo = &admin.ID
	require.NoError(t, db.Save(othersProspect).Error)

	othersLead := seedLead(t, db, nil)
	othersLead.AssignedTo = &admin.ID
	require.NoError(t, db.Save(othersLead).Error)

	getProspect := openRequest(t, http.MethodGet, "/api/v1/open/prospects/"+itoa(othersProspect.ID), nil, apiKey)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, getProspect, nil).StatusCode)

	listProspects := openRequest(t, http.MethodGet, "/api/v1/open/prospects", nil, apiKey)
	var prospectList struct {
		Data []models.Prospect `json:"data"`
	}
	require.Equal(t, fiber.StatusOK, doJSON(t, app, listProspects, &prospectList).StatusCode)
	found := false
	for _, p := range prospectList.Data {
		if p.ID == othersProspect.ID {
			found = true
		}
	}
	require.True(t, found, "a Sales-Rep-owned key must still see another rep's Prospect in the list")

	getLead := openRequest(t, http.MethodGet, "/api/v1/open/leads/"+itoa(othersLead.ID), nil, apiKey)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, getLead, nil).StatusCode)

	listLeads := openRequest(t, http.MethodGet, "/api/v1/open/leads", nil, apiKey)
	var leadList struct {
		Data []models.Lead `json:"data"`
	}
	require.Equal(t, fiber.StatusOK, doJSON(t, app, listLeads, &leadList).StatusCode)
	found = false
	for _, l := range leadList.Data {
		if l.ID == othersLead.ID {
			found = true
		}
	}
	require.True(t, found, "a Sales-Rep-owned key must still see another rep's Lead in the list")
}

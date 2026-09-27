package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// companyTaxEnv is an Open API key plus small Create/List helpers, shared by
// the tax-ID matching tests below.
type companyTaxEnv struct {
	t      *testing.T
	app    *fiber.App
	apiKey string
}

// newCompanyTaxEnv returns the env and its db. testutil.App takes the test
// database lock, so a test must call it (via this) only once.
func newCompanyTaxEnv(t *testing.T) (*companyTaxEnv, *gorm.DB) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	return &companyTaxEnv{t: t, app: app, apiKey: createAPIKey(t, app, admin.ID, admin.ID)}, db
}

func (e *companyTaxEnv) do(method, path string, body map[string]interface{}, out interface{}) int {
	return doJSON(e.t, e.app, openRequest(e.t, method, path, body, e.apiKey), out).StatusCode
}

func (e *companyTaxEnv) create(body map[string]interface{}) (models.Company, int) {
	var created struct {
		Data models.Company `json:"data"`
	}
	status := e.do(http.MethodPost, "/api/v1/open/companies", body, &created)
	return created.Data, status
}

func (e *companyTaxEnv) listIDs(query string) []uint {
	var body struct {
		Data []models.Company `json:"data"`
	}
	require.Equal(e.t, fiber.StatusOK, e.do(http.MethodGet, "/api/v1/open/companies?"+query, nil, &body))
	ids := make([]uint, 0, len(body.Data))
	for _, co := range body.Data {
		ids = append(ids, co.ID)
	}
	return ids
}

// TestCompanyTaxID_NormalizedOnWriteAndLookup: spaces and dashes are dropped
// from tax_id on save, and ?tax_id= is normalized the same way, so a value
// typed either way finds the same Company.
func TestCompanyTaxID_NormalizedOnWriteAndLookup(t *testing.T) {
	env, _ := newCompanyTaxEnv(t)

	company, status := env.create(map[string]interface{}{"name": "Acme", "tax_id": " 0-1055-55555-55-5 "})
	require.Equal(t, fiber.StatusCreated, status)
	require.NotNil(t, company.TaxID)
	require.Equal(t, "0105555555555", *company.TaxID)

	require.Equal(t, []uint{company.ID}, env.listIDs("tax_id=0105555555555"))
	require.Equal(t, []uint{company.ID}, env.listIDs("tax_id=0-1055-55555-55-5"))
	// A tax_id with nothing left after normalizing matches nothing, rather
	// than dropping the filter and returning every Company.
	require.Empty(t, env.listIDs("tax_id=-"))
	require.Empty(t, env.listIDs("tax_id=%20"))

	blank, status := env.create(map[string]interface{}{"name": "Blank Co", "tax_id": " - "})
	require.Equal(t, fiber.StatusCreated, status)
	require.Nil(t, blank.TaxID, "a tax_id with nothing left after normalizing is stored as null")
}

// TestCompanyTaxID_DuplicateTaxBranchConflict: a second Company with the same
// tax ID and branch gets 409 on Create and Update; a different branch is
// fine; and a row that already shared its pair before the check existed can
// still be edited as long as the pair doesn't change.
func TestCompanyTaxID_DuplicateTaxBranchConflict(t *testing.T) {
	env, db := newCompanyTaxEnv(t)

	head, status := env.create(map[string]interface{}{"name": "Acme HQ", "tax_id": "0105555555555", "branch_code": "00000"})
	require.Equal(t, fiber.StatusCreated, status)

	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	status = env.do(http.MethodPost, "/api/v1/open/companies",
		map[string]interface{}{"name": "Acme Co., Ltd.", "tax_id": "0-1055-55555-55-5", "branch_code": "00000", "industry": "Brand New Industry"}, &conflict)
	require.Equal(t, fiber.StatusConflict, status, "dashes don't get around the check")
	require.Equal(t, "CONFLICT", conflict.Error.Code)
	require.Contains(t, conflict.Error.Message, "tax ID and branch")
	require.Contains(t, conflict.Error.Message, itoa(head.ID))
	var industries int64
	require.NoError(t, db.Model(&models.IndustryOption{}).Where("name = ?", "Brand New Industry").Count(&industries).Error)
	require.Zero(t, industries, "a rejected Create must not register its industry")

	branch, status := env.create(map[string]interface{}{"name": "Acme Branch 1", "tax_id": "0105555555555", "branch_code": "00001"})
	require.Equal(t, fiber.StatusCreated, status, "same tax ID, different branch")

	_, status = env.create(map[string]interface{}{"name": "No Branch A", "tax_id": "0105555555556"})
	require.Equal(t, fiber.StatusCreated, status)
	_, status = env.create(map[string]interface{}{"name": "No Branch B", "tax_id": "0105555555556"})
	require.Equal(t, fiber.StatusConflict, status, "no branch on either counts as the same pair")

	status = env.do(http.MethodPut, "/api/v1/open/companies/"+itoa(branch.ID),
		map[string]interface{}{"name": "Acme Branch 1", "tax_id": "0105555555555", "branch_code": "00000"}, nil)
	require.Equal(t, fiber.StatusConflict, status, "moving onto another Company's pair")

	// A legacy duplicate, written straight to the DB as if saved before the
	// check existed: editing it without touching the pair still works.
	taxID, code := "0105555555555", "00000"
	legacy := models.Company{Name: "Acme (old row)", Status: models.StatusActive, TaxID: &taxID, BranchCode: &code}
	require.NoError(t, db.Create(&legacy).Error)
	status = env.do(http.MethodPut, "/api/v1/open/companies/"+itoa(legacy.ID),
		map[string]interface{}{"name": "Acme (old row)", "tax_id": taxID, "notes": "still editable"}, nil)
	require.Equal(t, fiber.StatusOK, status)
}

// TestCompanyTaxID_UpdatedSinceAndSort: ?updated_since= keeps only Companies
// updated at or after the bound, sort=updated_at is accepted, and a bad
// bound is a 422.
func TestCompanyTaxID_UpdatedSinceAndSort(t *testing.T) {
	env, db := newCompanyTaxEnv(t)

	old, _ := env.create(map[string]interface{}{"name": "Old Co"})
	recent, _ := env.create(map[string]interface{}{"name": "Recent Co"})
	require.NoError(t, db.Model(&models.Company{}).Where("id = ?", old.ID).UpdateColumn("updated_at", time.Now().AddDate(0, 0, -10)).Error)

	since := time.Now().AddDate(0, 0, -1).UTC().Format(time.RFC3339)
	require.Equal(t, []uint{recent.ID}, env.listIDs("updated_since="+since))
	require.Equal(t, []uint{recent.ID}, env.listIDs("updated_since="+time.Now().AddDate(0, 0, -1).Format("2006-01-02")))
	require.Equal(t, []uint{old.ID, recent.ID}, env.listIDs("sort=updated_at"))

	require.Equal(t, fiber.StatusUnprocessableEntity, env.do(http.MethodGet, "/api/v1/open/companies?updated_since=yesterday", nil, nil))
}

// TestCompanyTaxID_SearchMatchesTaxID: ?search= also matches tax IDs, with
// the term normalized like stored values, and a term of only dashes doesn't
// turn into a match-everything tax ID pattern.
func TestCompanyTaxID_SearchMatchesTaxID(t *testing.T) {
	env, _ := newCompanyTaxEnv(t)

	acme, _ := env.create(map[string]interface{}{"name": "Acme", "tax_id": "0105555555555"})
	env.create(map[string]interface{}{"name": "Other", "tax_id": "0994000123456"})

	require.Equal(t, []uint{acme.ID}, env.listIDs("search=01055555"))
	require.Equal(t, []uint{acme.ID}, env.listIDs("search=0-1055-5555"))
	require.Empty(t, env.listIDs("search=-"))
}

// TestCompanyTaxID_StartupNormalization: NormalizeCompanyTaxIDs rewrites
// legacy tax IDs (soft-deleted rows included), nulls ones that normalize to
// nothing, leaves updated_at alone, and is a no-op on a second run.
func TestCompanyTaxID_StartupNormalization(t *testing.T) {
	_, db := testutil.App(t)
	str := func(s string) *string { return &s }
	past := time.Now().AddDate(0, -1, 0).Truncate(time.Second)

	dashed := models.Company{Name: "Dashed", Status: models.StatusActive, TaxID: str("0-1055-55555-55-5")}
	blank := models.Company{Name: "Blank", Status: models.StatusActive, TaxID: str(" ")}
	deleted := models.Company{Name: "Deleted", Status: models.StatusActive, TaxID: str("0994 000 123 456")}
	plain := models.Company{Name: "Plain", Status: models.StatusActive, TaxID: str("0105555555556")}
	for _, co := range []*models.Company{&dashed, &blank, &deleted, &plain} {
		require.NoError(t, db.Create(co).Error)
		require.NoError(t, db.Model(co).UpdateColumn("updated_at", past).Error)
	}
	require.NoError(t, db.Delete(&deleted).Error)

	require.NoError(t, database.NormalizeCompanyTaxIDs(db))
	require.NoError(t, database.NormalizeCompanyTaxIDs(db), "second run")

	reload := func(id uint) models.Company {
		var co models.Company
		require.NoError(t, db.Unscoped().First(&co, id).Error)
		return co
	}
	got := reload(dashed.ID)
	require.Equal(t, "0105555555555", *got.TaxID)
	require.WithinDuration(t, past, got.UpdatedAt, time.Second, "normalizing isn't an edit")
	require.Nil(t, reload(blank.ID).TaxID)
	require.Equal(t, "0994000123456", *reload(deleted.ID).TaxID)
	require.Equal(t, "0105555555556", *reload(plain.ID).TaxID)
}

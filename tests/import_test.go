package apitests

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// csvImportRequest builds a POST request uploading csvContent as the `file`
// multipart field, matching what ImportCompanies/ImportContacts expect.
func csvImportRequest(t *testing.T, path, csvContent, token string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "import.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte(csvContent))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// TestImportCompanies_DedupesWithinSameFile proves the batched rewrite still
// dedupes two rows in the *same* file that resolve to the same company (here,
// by domain) — the second row must update the company the first row just
// created, not create a duplicate. This is the case an in-memory-map
// preloaded-once-per-import approach could easily get wrong if it only
// checked the DB state from before the request started.
func TestImportCompanies_DedupesWithinSameFile(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)

	csvContent := "name,industry,size,website\n" +
		"Acme Co,Tech,Small,https://acme.com\n" +
		"Acme Corp,Tech,Medium,https://www.acme.com/about\n"

	req := csvImportRequest(t, "/api/v1/companies/import", csvContent, token)
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, body.Data.Created, "first row creates the company")
	assert.Equal(t, 1, body.Data.Updated, "second row (same domain) updates it instead of creating a duplicate")

	var count int64
	db.Model(&models.Company{}).Where("domain = ?", "acme.com").Count(&count)
	assert.Equal(t, int64(1), count, "exactly one company should exist for this domain")

	var company models.Company
	require.NoError(t, db.Where("domain = ?", "acme.com").First(&company).Error)
	assert.Equal(t, "Medium", company.Size, "the later row's data should have won")
}

// TestImportCompanies_UpdatesExistingRow proves a row matching a company
// that already existed before the import (not just one created earlier in
// the same file) is still found via the preloaded index and updated in place.
func TestImportCompanies_UpdatesExistingRow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)

	// Domain is set explicitly, matching how CompanyHandler.Create/Update
	// always populate it (utils.ExtractDomain) — the column is what the
	// import's domain lookup actually matches against, same as it was before
	// this batching rewrite.
	existing := &models.Company{Name: "Beta Inc", Website: "https://beta.com", Domain: "beta.com", Status: models.StatusActive}
	require.NoError(t, db.Create(existing).Error)

	csvContent := "name,industry,size,website\nBeta Incorporated,Finance,Large,https://beta.com\n"
	req := csvImportRequest(t, "/api/v1/companies/import", csvContent, token)
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, body.Data.Created)
	assert.Equal(t, 1, body.Data.Updated)

	var reloaded models.Company
	require.NoError(t, db.First(&reloaded, existing.ID).Error)
	assert.Equal(t, "Finance", reloaded.Industry)
}

// TestImportContacts_DedupesByEmailWithinSameFile mirrors the companies test
// for the email-keyed contact import path.
func TestImportContacts_DedupesByEmailWithinSameFile(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	company := seedCompany(t, db)

	csvContent := "company_id,name,email,phone,role_title\n" +
		itoa(company.ID) + ",Jane Doe,jane@example.com,111,Manager\n" +
		itoa(company.ID) + ",Jane D.,jane@example.com,222,Director\n"

	req := csvImportRequest(t, "/api/v1/contacts/import", csvContent, token)
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, body.Data.Created)
	assert.Equal(t, 1, body.Data.Updated)

	var count int64
	db.Model(&models.Contact{}).Where("email = ?", "jane@example.com").Count(&count)
	assert.Equal(t, int64(1), count)
}

// TestImportCompanies_RejectsOversizedRowCount guards the new maxImportRows
// cap — a file with more rows than the limit must be rejected outright
// rather than processed (which used to mean an unbounded number of
// sequential DB round trips inside one request).
func TestImportCompanies_RejectsOversizedRowCount(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)

	var buf bytes.Buffer
	buf.WriteString("name,industry,size,website\n")
	for i := 0; i < 5001; i++ {
		buf.WriteString("Company,Tech,Small,\n")
	}

	req := csvImportRequest(t, "/api/v1/companies/import", buf.String(), token)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestImportContacts_MatchesWithinCompanyAndKeepsBlankCells guards the
// contact import's match key: lower(email) within the row's own Company. A
// same-email Contact in another Company is left alone (a new one is
// created, nothing moves), and an empty phone/role_title cell keeps the
// stored value.
func TestImportContacts_MatchesWithinCompanyAndKeepsBlankCells(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	companyA := seedCompany(t, db)
	companyB := seedCompany(t, db)
	inA := &models.Contact{CompanyID: companyA.ID, Name: "Jane", Email: "Jane@Example.com", Phone: "021111111", RoleTitle: "Manager", Status: models.StatusActive}
	require.NoError(t, db.Create(inA).Error)

	csvContent := "company_id,name,email,phone,role_title\n" +
		itoa(companyA.ID) + ",Jane Doe,jane@example.com,,\n" +
		itoa(companyB.ID) + ",Jane B,JANE@example.com,022222222,\n"
	var body struct {
		Data struct {
			Created int `json:"created"`
			Updated int `json:"updated"`
		} `json:"data"`
	}
	resp := doJSON(t, app, csvImportRequest(t, "/api/v1/contacts/import", csvContent, token), &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, body.Data.Updated)
	assert.Equal(t, 1, body.Data.Created)

	var reloaded models.Contact
	require.NoError(t, db.First(&reloaded, inA.ID).Error)
	assert.Equal(t, companyA.ID, reloaded.CompanyID, "never moved to another company")
	assert.Equal(t, "Jane Doe", reloaded.Name)
	assert.Equal(t, "021111111", reloaded.Phone, "empty cell keeps phone")
	assert.Equal(t, "Manager", reloaded.RoleTitle, "empty cell keeps role_title")

	var inB models.Contact
	require.NoError(t, db.Where("company_id = ?", companyB.ID).First(&inB).Error)
	assert.Equal(t, "Jane B", inB.Name)
}

// TestImportContacts_RejectsUnknownCompanyUpFront guards that a company_id
// naming no Company is a 422 before any row is written.
func TestImportContacts_RejectsUnknownCompanyUpFront(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	company := seedCompany(t, db)

	csvContent := "company_id,name,email,phone,role_title\n" +
		itoa(company.ID) + ",Good Row,good@example.com,,\n" +
		"999999,Bad Row,bad@example.com,,\n"
	resp := doJSON(t, app, csvImportRequest(t, "/api/v1/contacts/import", csvContent, token), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var n int64
	require.NoError(t, db.Model(&models.Contact{}).Count(&n).Error)
	assert.Equal(t, int64(0), n, "nothing imported")
}

// TestImport_BadRowIsSkippedNotFatal guards the per-row savepoint: a row
// Postgres rejects used to abort the transaction, failing every later row
// and the whole import with a 500. Now it's reported and skipped, and the
// rows around it are imported. The rejection comes from a CHECK constraint
// added for the test; a NUL byte, which no text column accepts, is skipped
// at parse time.
func TestImport_BadRowIsSkippedNotFatal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	token := testutil.Token(t, admin.ID, admin.Role)
	company := seedCompany(t, db)

	type result struct {
		Data struct {
			Created int `json:"created"`
			Skipped int `json:"skipped"`
			Errors  []struct {
				Row     int    `json:"row"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"data"`
	}
	rejectName := func(table, name string) {
		t.Helper()
		constraint := "test_reject_" + table
		require.NoError(t, db.Exec("ALTER TABLE "+table+" ADD CONSTRAINT "+constraint+" CHECK (name <> '"+name+"')").Error)
		t.Cleanup(func() { db.Exec("ALTER TABLE " + table + " DROP CONSTRAINT IF EXISTS " + constraint) })
	}

	t.Run("contacts", func(t *testing.T) {
		rejectName("contacts", "Rejected")
		csvContent := "company_id,name,email,phone,role_title\n" +
			itoa(company.ID) + ",First,first@example.com,,\n" +
			itoa(company.ID) + ",Rejected,bad@example.com,,\n" +
			itoa(company.ID) + ",Nul\x00Name,nul@example.com,,\n" +
			itoa(company.ID) + ",Fourth,fourth@example.com,,\n"
		var body result
		resp := doJSON(t, app, csvImportRequest(t, "/api/v1/contacts/import", csvContent, token), &body)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, body.Data.Created)
		assert.Equal(t, 2, body.Data.Skipped)
		require.Len(t, body.Data.Errors, 2)
		assert.Equal(t, 4, body.Data.Errors[0].Row, "NUL row is skipped while parsing")
		assert.Equal(t, 3, body.Data.Errors[1].Row)
		assert.Equal(t, "failed to create", body.Data.Errors[1].Message)
	})

	t.Run("companies", func(t *testing.T) {
		rejectName("companies", "Rejected Co")
		csvContent := "name,industry,size,website\n" +
			"First Co,Tech,Small,\n" +
			"Rejected Co,Tech,Small,\n" +
			"Nul\x00Co,Tech,Small,\n" +
			"Fourth Co,Tech,Small,\n"
		var body result
		resp := doJSON(t, app, csvImportRequest(t, "/api/v1/companies/import", csvContent, token), &body)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, body.Data.Created)
		assert.Equal(t, 2, body.Data.Skipped)
		require.Len(t, body.Data.Errors, 2)
		assert.Equal(t, 4, body.Data.Errors[0].Row, "NUL row is skipped while parsing")
		assert.Equal(t, 3, body.Data.Errors[1].Row)
		assert.Equal(t, "failed to create", body.Data.Errors[1].Message)
	})
}

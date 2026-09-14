package apitests

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestExport_CompaniesSanitizesFormulaInjection guards the CSV/formula-
// injection fix (CWE-1236): a Company name (or any other free-text export
// field) starting with a spreadsheet formula trigger character must be
// exported with a leading `'` so Excel/Sheets renders it as inert text
// instead of executing it as a live formula when an Admin/Sales Manager
// opens the file. Every one of these characters is checked, not just `=`,
// since Excel treats `+`/`-`/`@` (and a leading tab/CR) the same way.
func TestExport_CompaniesSanitizesFormulaInjection(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	dangerous := []string{
		`=HYPERLINK("http://evil.example","x")`,
		`+1+1`,
		`-1+1`,
		`@SUM(1,1)`,
	}
	for _, name := range dangerous {
		require.NoError(t, db.Create(&models.Company{Name: name, Status: models.StatusActive}).Error)
	}
	// A normal name must NOT be prefixed — only the dangerous ones should be.
	require.NoError(t, db.Create(&models.Company{Name: "Acme Corp", Status: models.StatusActive}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies/export", nil, admin.ID, models.RoleAdmin)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	// Parse as real CSV (rather than substring-matching the raw body) so
	// quoting/escaping of the HYPERLINK example's own embedded quotes
	// doesn't produce a flaky assertion.
	reader := csv.NewReader(resp.Body)
	rows, err := reader.ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, rows)

	nameByRow := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] { // skip header
		require.NotEmpty(t, row)
		nameByRow = append(nameByRow, row[0])
	}

	for _, name := range dangerous {
		require.Contains(t, nameByRow, "'"+name, "expected %q to be exported prefixed with a quote to defuse it as a formula", name)
		require.NotContains(t, nameByRow, name, "dangerous value %q must never appear unsanitized in a Name field", name)
	}
	require.Contains(t, nameByRow, "Acme Corp")
	require.NotContains(t, nameByRow, "'Acme Corp")
}

// TestExport_CSVFieldsAreQuotedSafely is a lighter-weight companion check
// that a field merely containing (not starting with) a formula-trigger
// character is left untouched — sanitization only needs to act on the
// leading character, not scrub the whole field.
func TestExport_CSVFieldsAreQuotedSafely(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	require.NoError(t, db.Create(&models.Company{Name: "Notes with = midway", Status: models.StatusActive}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/companies/export", nil, admin.ID, models.RoleAdmin)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	reader := csv.NewReader(resp.Body)
	rows, err := reader.ReadAll()
	require.NoError(t, err)
	found := false
	for _, row := range rows[1:] {
		if strings.HasPrefix(row[0], "Notes with =") {
			found = true
		}
	}
	require.True(t, found, "a field with = in the middle (not leading) should be exported unmodified")
}

// TestExport_NegativeNumbersNotSanitized guards the fix for a false positive
// in the formula-injection guard: a genuinely negative number (this
// package's own strconv.FormatFloat output for a negative Deal Value,
// OutstandingAmount, etc.) must NOT be prefixed with a `'` — that would
// silently turn a numeric column into text and break spreadsheet SUM/
// arithmetic on it for every row with a negative value. Only a leading +/-
// that ISN'T a plain number (a DDE-style "-2+3+cmd|...' " payload) should
// still be guarded — see TestExport_CompaniesSanitizesFormulaInjection's
// `+1+1`/`-1+1` cases, which cover that side.
func TestExport_NegativeNumbersNotSanitized(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	deal := &models.Deal{
		CompanyID: company.ID, ContactID: contact.ID, Title: "Refund Adjustment",
		Value: -500.25, Stage: models.DealStageLead, Status: models.DealStatusOpen,
	}
	require.NoError(t, db.Create(deal).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/export", nil, admin.ID, models.RoleAdmin)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	reader := csv.NewReader(resp.Body)
	rows, err := reader.ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, rows)

	found := false
	for _, row := range rows[1:] {
		if row[0] == "Refund Adjustment" {
			found = true
			require.Equal(t, "-500.25", row[2], "Value column")
		}
	}
	require.True(t, found)
}

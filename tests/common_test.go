// Package apitests holds black-box integration tests that exercise the whole
// stack — routes.Setup wired to real handlers, running against an isolated
// "sales_system_test" Postgres database — via Fiber's in-process app.Test,
// so no real TCP listener ever binds to :8080.
package apitests

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

func itoa(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func seedCompany(t *testing.T, db *gorm.DB) *models.Company {
	t.Helper()
	company := &models.Company{Name: "Acme Corp", Status: models.StatusActive}
	require.NoError(t, db.Create(company).Error)
	return company
}

func seedContact(t *testing.T, db *gorm.DB, companyID uint) *models.Contact {
	t.Helper()
	contact := &models.Contact{CompanyID: companyID, Name: "Jane Doe", Status: models.StatusActive}
	require.NoError(t, db.Create(contact).Error)
	return contact
}

// seedDeal creates a Company+Contact+Deal, assigned to assignedTo (nil = unassigned).
func seedDeal(t *testing.T, db *gorm.DB, assignedTo *uint) *models.Deal {
	t.Helper()
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	deal := &models.Deal{
		CompanyID:  company.ID,
		ContactID:  contact.ID,
		Title:      "Test Deal",
		Value:      1000,
		Stage:      models.DealStageLead,
		Status:     models.DealStatusOpen,
		AssignedTo: assignedTo,
	}
	require.NoError(t, db.Create(deal).Error)
	return deal
}

// doJSON runs req through app and decodes the JSON body into out (if non-nil).
func doJSON(t *testing.T, app *fiber.App, req *http.Request, out interface{}) *http.Response {
	t.Helper()
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	if out != nil {
		testutil.DecodeJSON(t, resp, out)
	}
	return resp
}

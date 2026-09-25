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
	"github.com/stretchr/testify/assert"
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

// seedProspect creates a Prospect, optionally linked to a Company (nil =
// unlinked), mirroring seedLead's nilable-companyID convention.
func seedProspect(t *testing.T, db *gorm.DB, companyID *uint) *models.Prospect {
	t.Helper()
	prospect := &models.Prospect{
		Name: "Riley Chen", CompanyID: companyID,
		Source: "Social Media", Status: models.ProspectStatusEngaging,
	}
	require.NoError(t, db.Create(prospect).Error)
	return prospect
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

// keepSeedConfig snapshots the seed-once config tables (pipeline_stages,
// prospect_stages, app_settings — not truncated between tests, see
// testutil) and restores them when the test ends, for a test that renames a
// stage, sets a stale threshold, or flips a setting. Without it the change
// leaks into every later test in the run.
func keepSeedConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	var stages []models.PipelineStage
	var prospectStages []models.ProspectStage
	var settings []models.AppSettings
	require.NoError(t, db.Unscoped().Find(&stages).Error)
	require.NoError(t, db.Unscoped().Find(&prospectStages).Error)
	require.NoError(t, db.Find(&settings).Error)
	t.Cleanup(func() {
		restoreRows(t, db, &models.PipelineStage{}, stages, func(s models.PipelineStage) uint { return s.ID })
		restoreRows(t, db, &models.ProspectStage{}, prospectStages, func(s models.ProspectStage) uint { return s.ID })
		for i := range settings {
			assert.NoError(t, db.Save(&settings[i]).Error, "restore app_settings")
		}
	})
}

// restoreRows puts a stage table back to snapshot: rows the test added are
// deleted, and every snapshot row is first parked under a unique temporary
// name so restoring swapped names can't trip the unique name index.
func restoreRows[T any](t *testing.T, db *gorm.DB, model interface{}, snapshot []T, id func(T) uint) {
	t.Helper()
	ids := make([]uint, 0, len(snapshot))
	for _, row := range snapshot {
		ids = append(ids, id(row))
	}
	q := db.Unscoped()
	if len(ids) > 0 {
		q = q.Where("id NOT IN ?", ids)
	} else {
		q = q.Where("1 = 1")
	}
	assert.NoError(t, q.Delete(model).Error, "delete rows added by the test")
	assert.NoError(t, db.Unscoped().Model(model).Where("id IN ?", ids).
		UpdateColumn("name", gorm.Expr("'__restore_' || id")).Error, "park names")
	for i := range snapshot {
		assert.NoError(t, db.Unscoped().Save(&snapshot[i]).Error, "restore row")
	}
}

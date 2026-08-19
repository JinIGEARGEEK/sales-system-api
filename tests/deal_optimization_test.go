package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDealCreate_RejectsNegativeValue guards the new value>=0 validation
// (previously Deal.Value had no range check at all, so a negative number
// would silently corrupt every SUM(value) dashboard/report aggregate).
func TestDealCreate_RejectsNegativeValue(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"company_id": company.ID,
		"contact_id": contact.ID,
		"title":      "Negative value deal",
		"value":      -500,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestDealCreate_RejectsMalformedExpectedCloseDate guards the new date-format
// validation — previously any string persisted untouched and would silently
// never land in a forecastTrend month bucket instead of erroring at write time.
func TestDealCreate_RejectsMalformedExpectedCloseDate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"company_id":          company.ID,
		"contact_id":          contact.ID,
		"title":               "Bad date deal",
		"expected_close_date": "not-a-date",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestDealCreate_AcceptsBothExpectedCloseDateShapes confirms both date shapes
// the frontend actually sends (plain YYYY-MM-DD and full ISO datetime) still
// pass validation, so the new check doesn't reject legitimate submissions.
func TestDealCreate_AcceptsBothExpectedCloseDateShapes(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	for _, date := range []string{"2026-09-01", "2026-09-01T00:00:00.000Z"} {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
			"company_id":          company.ID,
			"contact_id":          contact.ID,
			"title":               "Good date deal",
			"expected_close_date": date,
		}, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusCreated, resp.StatusCode, "date %q should be accepted", date)
	}
}

// TestDealsExport_RespectsFilters guards the shared applyDealFilters helper
// (export previously duplicated List's filter block and had already drifted,
// missing the assigned_to=unassigned case) and the new streamed CSV response.
func TestDealsExport_RespectsFilters(t *testing.T) {
	app, db := testutil.App(t)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	assigned := seedDeal(t, db, &rep.ID)
	assigned.Title = "Assigned Deal"
	require.NoError(t, db.Save(assigned).Error)

	unassigned := seedDeal(t, db, nil)
	unassigned.Title = "Unassigned Deal"
	require.NoError(t, db.Save(unassigned).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/export?assigned_to=unassigned", nil, manager.ID, manager.Role)
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/csv", resp.Header.Get("Content-Type"))

	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if readErr != nil {
			break
		}
	}
	csvText := string(body)
	assert.Contains(t, csvText, unassigned.Title)
	assert.NotContains(t, csvText, assigned.Title)
}

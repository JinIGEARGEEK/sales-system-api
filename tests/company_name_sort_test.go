package apitests

import (
	"net/http"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestCompanyNameSort_WithFilters guards sort=company_name alongside the
// list filters: utils.ApplyCompanyNameSort joins companies, which shares
// status/name/email/tags with deals/contacts, so an unqualified filter
// column was a 500 ("column reference is ambiguous"). Rows under the same
// Company come back in id order (the tie-breaker), so paging is stable.
func TestCompanyNameSort_WithFilters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	company := seedCompany(t, db)
	bu := models.BusinessUnitProduct
	var dealIDs, contactIDs []uint
	for i := 0; i < 3; i++ {
		contact := &models.Contact{CompanyID: company.ID, Name: "Jane Doe", Email: "jane@example.com", Status: models.StatusActive, Tags: pq.StringArray{"vip"}}
		require.NoError(t, db.Create(contact).Error)
		contactIDs = append(contactIDs, contact.ID)
		deal := &models.Deal{CompanyID: company.ID, ContactID: contact.ID, Title: "Deal", Value: 1,
			Stage: models.DealStageLead, Status: models.DealStatusOpen, BusinessUnit: &bu, Channel: models.LeadSourceWebsite}
		require.NoError(t, db.Create(deal).Error)
		dealIDs = append(dealIDs, deal.ID)
	}

	listIDs := func(path string) []uint {
		t.Helper()
		var out struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, path, nil, admin.ID, admin.Role), &out)
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		ids := make([]uint, len(out.Data))
		for i, r := range out.Data {
			ids[i] = r.ID
		}
		return ids
	}

	for _, sort := range []string{"company_name", "-company_name"} {
		got := listIDs("/api/v1/deals?sort=" + sort + "&status=open&stage=Lead&company_id=" + itoa(company.ID) +
			"&assigned_to=unassigned&business_unit=Product&channel=" + string(models.LeadSourceWebsite) + "&search=deal")
		want := append([]uint(nil), dealIDs...)
		if sort[0] == '-' {
			want = []uint{want[2], want[1], want[0]}
		}
		assert.Equal(t, want, got, "deals "+sort)

		got = listIDs("/api/v1/contacts?sort=" + sort + "&status=active&tag=vip&search=jane&company_id=" + itoa(company.ID))
		want = append([]uint(nil), contactIDs...)
		if sort[0] == '-' {
			want = []uint{want[2], want[1], want[0]}
		}
		assert.Equal(t, want, got, "contacts "+sort)
	}

	for _, path := range []string{
		"/api/v1/deals/export?sort=company_name&status=open&search=deal",
		"/api/v1/contacts/export?sort=company_name&status=active&tag=vip&search=jane",
	} {
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, path, nil, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
}

package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// duplicateBody is the 409 envelope utils.DuplicateConflict writes.
type duplicateBody struct {
	Error struct {
		Code        string              `json:"code"`
		Fields      map[string][]string `json:"fields"`
		DuplicateOf []uint              `json:"duplicate_of"`
	} `json:"error"`
}

// TestCreate_RejectsDuplicateEmailOrPhone covers Lead, Prospect and Contact
// Create: a case-insensitive email match or a normalized phone match (+66 =
// leading 0) is a 409 naming the field and the existing row, a soft-deleted
// row doesn't count, and ?allow_duplicate=true creates it anyway.
func TestCreate_RejectsDuplicateEmailOrPhone(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	lead := &models.Lead{Name: "Existing", Email: "Jane@Example.com", Phone: "081-234-5678", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew}
	require.NoError(t, db.Create(lead).Error)
	prospect := &models.Prospect{Name: "Existing", Email: "Jane@Example.com", Phone: "081-234-5678", Source: "Social Media", Status: models.ProspectStatusEngaging}
	require.NoError(t, db.Create(prospect).Error)
	contact := &models.Contact{CompanyID: company.ID, Name: "Existing", Email: "Jane@Example.com", Phone: "081-234-5678", Status: models.StatusActive}
	require.NoError(t, db.Create(contact).Error)

	cases := []struct {
		path       string
		existingID uint
		extra      map[string]interface{}
	}{
		{"/api/v1/leads", lead.ID, map[string]interface{}{"source": "Website"}},
		{"/api/v1/prospects", prospect.ID, map[string]interface{}{"source": "Social Media"}},
		{"/api/v1/contacts", contact.ID, map[string]interface{}{"company_id": company.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			body := func(email, phone string) map[string]interface{} {
				b := map[string]interface{}{"name": "New Person", "email": email, "phone": phone}
				for k, v := range tc.extra {
					b[k] = v
				}
				return b
			}

			var dup duplicateBody
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path, body("jane@EXAMPLE.com", ""), admin.ID, admin.Role), &dup)
			require.Equal(t, http.StatusConflict, resp.StatusCode)
			assert.Equal(t, "CONFLICT", dup.Error.Code)
			assert.Contains(t, dup.Error.Fields, "email")
			assert.NotContains(t, dup.Error.Fields, "phone")
			assert.Equal(t, []uint{tc.existingID}, dup.Error.DuplicateOf)

			dup = duplicateBody{}
			resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path, body("other@example.com", "+66 81 234 5678"), admin.ID, admin.Role), &dup)
			require.Equal(t, http.StatusConflict, resp.StatusCode)
			assert.Contains(t, dup.Error.Fields, "phone")
			assert.NotContains(t, dup.Error.Fields, "email")
			assert.Equal(t, []uint{tc.existingID}, dup.Error.DuplicateOf)

			resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path+"?allow_duplicate=true", body("jane@example.com", "0812345678"), admin.ID, admin.Role), nil)
			assert.Equal(t, http.StatusCreated, resp.StatusCode, "allow_duplicate=true overrides")

			resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path, body("unique@example.com", "02-999-9999"), admin.ID, admin.Role), nil)
			assert.Equal(t, http.StatusCreated, resp.StatusCode, "no match is created as before")
		})
	}

	// Soft-deleted rows don't count.
	require.NoError(t, db.Where("1 = 1").Delete(&models.Lead{}).Error)
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads",
		map[string]interface{}{"name": "Again", "email": "jane@example.com", "source": "Website"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// TestLeadProspect_ValidateAssignee guards assigned_to on Create/Update: an
// inactive user or one outside the sales roles is a 422 on assigned_to,
// while an Update that resends an owner who has since been deactivated
// still saves.
func TestLeadProspect_ValidateAssignee(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	inactive := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(inactive).Update("is_active", false).Error)

	for _, tc := range []struct{ path, source string }{
		{"/api/v1/leads", "Website"},
		{"/api/v1/prospects", "Social Media"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, bad := range []uint{production.ID, inactive.ID, 999999} {
				var out struct {
					Error struct {
						Fields map[string][]string `json:"fields"`
					} `json:"error"`
				}
				resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path,
					map[string]interface{}{"name": "X", "source": tc.source, "assigned_to": bad}, admin.ID, admin.Role), &out)
				require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "assignee %d", bad)
				assert.Contains(t, out.Error.Fields, "assigned_to")
			}

			var created struct {
				Data struct {
					ID uint `json:"id"`
				} `json:"data"`
			}
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path,
				map[string]interface{}{"name": "Owned", "source": tc.source, "assigned_to": rep.ID}, admin.ID, admin.Role), &created)
			require.Equal(t, http.StatusCreated, resp.StatusCode)

			resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, tc.path+"/"+itoa(created.Data.ID),
				map[string]interface{}{"name": "Owned", "source": tc.source, "assigned_to": production.ID}, admin.ID, admin.Role), nil)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "changing to a non-sales user")

			require.NoError(t, db.Model(&models.User{}).Where("id = ?", rep.ID).Update("is_active", false).Error)
			t.Cleanup(func() { db.Model(&models.User{}).Where("id = ?", rep.ID).Update("is_active", true) })
			resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, tc.path+"/"+itoa(created.Data.ID),
				map[string]interface{}{"name": "Renamed", "source": tc.source, "assigned_to": rep.ID}, admin.ID, admin.Role), nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode, "an unchanged (now inactive) owner doesn't block the edit")
			require.NoError(t, db.Model(&models.User{}).Where("id = ?", rep.ID).Update("is_active", true).Error)
		})
	}
}

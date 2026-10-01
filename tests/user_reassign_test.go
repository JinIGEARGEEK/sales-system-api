package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// openCounts mirrors the handlers' openRecordCounts JSON.
type openCounts struct {
	UserID     uint  `json:"user_id"`
	ReassignTo uint  `json:"reassign_to"`
	Deals      int64 `json:"deals"`
	Leads      int64 `json:"leads"`
	Prospects  int64 `json:"prospects"`
	Tasks      int64 `json:"tasks"`
	Total      int64 `json:"total"`
}

// ownedRecords is what seedOwnedRecords created for one owner: one open
// record of each kind (counted/moved) plus closed ones that must stay put.
type ownedRecords struct {
	openDeal, wonDeal          *models.Deal
	openLead, convertedLead    models.Lead
	openProspect, lostProspect models.Prospect
	pendingTask, doneTask      models.Task
}

func seedOwnedRecords(t *testing.T, db *gorm.DB, owner uint) ownedRecords {
	t.Helper()
	var r ownedRecords
	r.openDeal = seedDeal(t, db, &owner)
	r.wonDeal = seedDeal(t, db, &owner)
	require.NoError(t, db.Model(r.wonDeal).Update("status", models.DealStatusWon).Error)

	r.openLead = models.Lead{Name: "Open Lead", Status: models.LeadStatusContacted, AssignedTo: &owner}
	r.convertedLead = models.Lead{Name: "Converted Lead", Status: models.LeadStatusQualified, AssignedTo: &owner, ConvertedDealID: &r.wonDeal.ID}
	require.NoError(t, db.Create(&r.openLead).Error)
	require.NoError(t, db.Create(&r.convertedLead).Error)

	r.openProspect = models.Prospect{Name: "Open Prospect", Source: "Social Media", Status: models.ProspectStatusEngaging, AssignedTo: &owner}
	r.lostProspect = models.Prospect{Name: "Lost Prospect", Source: "Social Media", Status: models.ProspectStatusDisqualified, AssignedTo: &owner}
	require.NoError(t, db.Create(&r.openProspect).Error)
	require.NoError(t, db.Create(&r.lostProspect).Error)

	due := time.Now().Add(24 * time.Hour)
	r.pendingTask = models.Task{RelatedType: "deal", RelatedID: r.openDeal.ID, Title: "Call", DueDate: due, Status: models.TaskStatusPending, AssignedTo: &owner}
	r.doneTask = models.Task{RelatedType: "deal", RelatedID: r.openDeal.ID, Title: "Done", DueDate: due, Status: models.TaskStatusDone, AssignedTo: &owner}
	require.NoError(t, db.Create(&r.pendingTask).Error)
	require.NoError(t, db.Create(&r.doneTask).Error)
	return r
}

// assigneeOf reads row's assigned_to straight from the DB (soft-deleted too).
func assigneeOf(t *testing.T, db *gorm.DB, model interface{}, id uint) *uint {
	t.Helper()
	var out struct{ AssignedTo *uint }
	require.NoError(t, db.Unscoped().Model(model).Select("assigned_to").Where("id = ?", id).Take(&out).Error)
	return out.AssignedTo
}

// assertMoved checks the open records went to `to` and the closed ones
// stayed with `from`.
func assertMoved(t *testing.T, db *gorm.DB, r ownedRecords, from, to uint) {
	t.Helper()
	for _, row := range []struct {
		model interface{}
		id    uint
		want  uint
	}{
		{&models.Deal{}, r.openDeal.ID, to}, {&models.Deal{}, r.wonDeal.ID, from},
		{&models.Lead{}, r.openLead.ID, to}, {&models.Lead{}, r.convertedLead.ID, from},
		{&models.Prospect{}, r.openProspect.ID, to}, {&models.Prospect{}, r.lostProspect.ID, from},
		{&models.Task{}, r.pendingTask.ID, to}, {&models.Task{}, r.doneTask.ID, from},
	} {
		got := assigneeOf(t, db, row.model, row.id)
		if assert.NotNil(t, got, "%T %d", row.model, row.id) {
			assert.Equal(t, row.want, *got, "%T %d", row.model, row.id)
		}
	}
}

func auditActions(t *testing.T, db *gorm.DB, entityType string, entityID uint) []string {
	t.Helper()
	var actions []string
	require.NoError(t, db.Model(&models.AuditLogEntry{}).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Order("id").Pluck("action", &actions).Error)
	return actions
}

func errField(body sessionErrBody, field string) bool {
	_, ok := body.Error.Fields[field]
	return ok
}

// TestUserDeactivate_ReportsOpenRecords — without reassign_to the user is
// deactivated as before, and the response says what they still own (open
// records only), so the UI can offer a reassign.
func TestUserDeactivate_ReportsOpenRecords(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	r := seedOwnedRecords(t, db, rep.ID)

	var out struct {
		Data struct {
			ID          uint        `json:"id"`
			IsActive    bool        `json:"is_active"`
			OpenRecords *openCounts `json:"open_records"`
			Reassigned  *openCounts `json:"reassigned"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(rep.ID),
		userUpdateBody(rep, map[string]string{"status": "inactive"}), admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)

	assert.Equal(t, rep.ID, out.Data.ID)
	assert.False(t, out.Data.IsActive)
	require.NotNil(t, out.Data.OpenRecords)
	assert.Equal(t, openCounts{Deals: 1, Leads: 1, Prospects: 1, Tasks: 1, Total: 4}, *out.Data.OpenRecords)
	assert.Nil(t, out.Data.Reassigned)
	assertMoved(t, db, r, rep.ID, rep.ID)
	assert.Equal(t, []string{"deactivated"}, auditActions(t, db, "user", rep.ID))

	// A plain edit doesn't take records away, so no open_records.
	var plain map[string]map[string]interface{}
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(rep.ID),
		userUpdateBody(rep, map[string]string{"first_name": "Renamed"}), admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, &plain).StatusCode)
	assert.NotContains(t, plain["data"], "open_records")
}

// TestUserDeactivate_ReassignTo moves the open records in the same
// transaction, leaves closed ones as history, and audits the hand-off.
func TestUserDeactivate_ReassignTo(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	heir := testutil.CreateUser(t, db, models.RoleSalesRep)
	r := seedOwnedRecords(t, db, rep.ID)

	body := map[string]interface{}{
		"first_name": rep.FirstName, "last_name": rep.LastName, "email": rep.Email,
		"role": rep.Role, "status": "inactive", "reassign_to": heir.ID,
	}
	var out struct {
		Data struct {
			OpenRecords *openCounts `json:"open_records"`
			Reassigned  *openCounts `json:"reassigned"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(rep.ID), body, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)

	require.NotNil(t, out.Data.Reassigned)
	assert.Equal(t, openCounts{UserID: rep.ID, ReassignTo: heir.ID, Deals: 1, Leads: 1, Prospects: 1, Tasks: 1, Total: 4}, *out.Data.Reassigned)
	require.NotNil(t, out.Data.OpenRecords)
	assert.Equal(t, int64(0), out.Data.OpenRecords.Total)
	assertMoved(t, db, r, rep.ID, heir.ID)

	var entry models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "user", rep.ID, "records_reassigned").Take(&entry).Error)
	assert.EqualValues(t, heir.ID, entry.After["assigned_to"])
	assert.EqualValues(t, 1, entry.After["deals"])
	assert.EqualValues(t, 1, entry.After["tasks"])
	assert.Equal(t, admin.ID, entry.ActorID)
}

// TestUserRoleChangeToProduction_ReassignTo — a move to a role that can't
// own pipeline records is treated like a deactivation for reassign_to.
func TestUserRoleChangeToProduction_ReassignTo(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	heir := testutil.CreateUser(t, db, models.RoleSalesManager)
	r := seedOwnedRecords(t, db, rep.ID)

	body := map[string]interface{}{
		"first_name": rep.FirstName, "email": rep.Email, "role": models.RoleProduction, "reassign_to": heir.ID,
	}
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(rep.ID), body, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
	assertMoved(t, db, r, rep.ID, heir.ID)

	var entry models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "user", rep.ID, "role_changed").Take(&entry).Error)
	assert.Equal(t, string(models.RoleSalesRep), entry.Before["role"])
	assert.Equal(t, string(models.RoleProduction), entry.After["role"])
}

// TestUserReassignTo_Invalid — reassign_to must be an active sales-role user
// other than the one losing the records; a bad one changes nothing.
func TestUserReassignTo_Invalid(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	inactive := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(inactive).Update("is_active", false).Error)
	r := seedOwnedRecords(t, db, rep.ID)

	for name, to := range map[string]uint{"production": production.ID, "inactive": inactive.ID, "self": rep.ID, "missing": 99999} {
		body := map[string]interface{}{
			"first_name": rep.FirstName, "email": rep.Email, "role": rep.Role, "status": "inactive", "reassign_to": to,
		}
		var errBody sessionErrBody
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(rep.ID), body, admin.ID, admin.Role)
		require.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, &errBody).StatusCode, name)
		assert.True(t, errField(errBody, "reassign_to"), name)

		req = testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(rep.ID)+"?reassign_to="+itoa(to), nil, admin.ID, admin.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "delete "+name)

		req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/users/bulk-deactivate",
			map[string]interface{}{"ids": []uint{rep.ID}, "reassign_to": to}, admin.ID, admin.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "bulk "+name)
	}

	var stored models.User
	require.NoError(t, db.First(&stored, rep.ID).Error)
	assert.True(t, stored.IsActive, "a rejected reassign_to must not deactivate the user")
	assertMoved(t, db, r, rep.ID, rep.ID)
}

// TestUserDelete_ReassignTo — DELETE takes reassign_to as a query param or
// JSON body and now answers 200 with the counts.
func TestUserDelete_ReassignTo(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	heir := testutil.CreateUser(t, db, models.RoleSalesRep)

	t.Run("query", func(t *testing.T) {
		rep := testutil.CreateUser(t, db, models.RoleSalesRep)
		r := seedOwnedRecords(t, db, rep.ID)
		var out struct {
			Data struct {
				ID          uint        `json:"id"`
				OpenRecords openCounts  `json:"open_records"`
				Reassigned  *openCounts `json:"reassigned"`
			} `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(rep.ID)+"?reassign_to="+itoa(heir.ID), nil, admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)
		assert.Equal(t, rep.ID, out.Data.ID)
		assert.Equal(t, int64(0), out.Data.OpenRecords.Total)
		require.NotNil(t, out.Data.Reassigned)
		assert.Equal(t, int64(4), out.Data.Reassigned.Total)
		assertMoved(t, db, r, rep.ID, heir.ID)
	})

	t.Run("body", func(t *testing.T) {
		rep := testutil.CreateUser(t, db, models.RoleSalesRep)
		r := seedOwnedRecords(t, db, rep.ID)
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(rep.ID), map[string]interface{}{"reassign_to": heir.ID}, admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
		assertMoved(t, db, r, rep.ID, heir.ID)
	})

	t.Run("none", func(t *testing.T) {
		rep := testutil.CreateUser(t, db, models.RoleSalesRep)
		seedOwnedRecords(t, db, rep.ID)
		var out struct {
			Data struct {
				OpenRecords openCounts `json:"open_records"`
			} `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(rep.ID), nil, admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)
		assert.Equal(t, int64(4), out.Data.OpenRecords.Total)
	})
}

// TestUserBulkDeactivate_ReassignTo — every listed user's open records move
// to reassign_to; the response lists what moved and what's left per user.
func TestUserBulkDeactivate_ReassignTo(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	a := testutil.CreateUser(t, db, models.RoleSalesRep)
	b := testutil.CreateUser(t, db, models.RoleMarketing)
	heir := testutil.CreateUser(t, db, models.RoleSalesManager)
	ra, rb := seedOwnedRecords(t, db, a.ID), seedOwnedRecords(t, db, b.ID)

	var out struct {
		Data struct {
			OpenRecords []openCounts `json:"open_records"`
			Reassigned  []openCounts `json:"reassigned"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/users/bulk-deactivate",
		map[string]interface{}{"ids": []uint{a.ID, b.ID}, "reassign_to": heir.ID}, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)
	require.Len(t, out.Data.Reassigned, 2)
	assert.Equal(t, int64(4), out.Data.Reassigned[0].Total)
	require.Len(t, out.Data.OpenRecords, 2)
	assert.Equal(t, int64(0), out.Data.OpenRecords[0].Total+out.Data.OpenRecords[1].Total)
	assertMoved(t, db, ra, a.ID, heir.ID)
	assertMoved(t, db, rb, b.ID, heir.ID)

	// Without reassign_to, the counts come back and nothing moves.
	c := testutil.CreateUser(t, db, models.RoleSalesRep)
	seedOwnedRecords(t, db, c.ID)
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/users/bulk-deactivate",
		map[string]interface{}{"ids": []uint{c.ID}}, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)
	require.Len(t, out.Data.OpenRecords, 1)
	assert.Equal(t, openCounts{UserID: c.ID, Deals: 1, Leads: 1, Prospects: 1, Tasks: 1, Total: 4}, out.Data.OpenRecords[0])
	assert.Empty(t, out.Data.Reassigned)

	// reassign_to may not be one of the users being deactivated.
	d := testutil.CreateUser(t, db, models.RoleSalesRep)
	e := testutil.CreateUser(t, db, models.RoleSalesRep)
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/users/bulk-deactivate",
		map[string]interface{}{"ids": []uint{d.ID, e.ID}, "reassign_to": e.ID}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode)
}

// TestUserSelfGuard — an Admin can't change their own role or deactivate or
// delete themselves (422); editing their own name still works.
func TestUserSelfGuard(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	testutil.CreateUser(t, db, models.RoleAdmin) // not the last Admin either way

	for name, overrides := range map[string]map[string]string{
		"role":   {"role": string(models.RoleSalesRep)},
		"status": {"status": "inactive"},
	} {
		var errBody sessionErrBody
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(admin.ID), userUpdateBody(admin, overrides), admin.ID, admin.Role)
		require.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, &errBody).StatusCode, name)
		assert.True(t, errField(errBody, name), name)
	}

	req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(admin.ID), nil, admin.ID, admin.Role)
	assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode)

	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/users/bulk-deactivate", map[string]interface{}{"ids": []uint{admin.ID}}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode)

	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(admin.ID),
		userUpdateBody(admin, map[string]string{"first_name": "Renamed"}), admin.ID, admin.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)

	var stored models.User
	require.NoError(t, db.First(&stored, admin.ID).Error)
	assert.True(t, stored.IsActive)
	assert.Equal(t, models.RoleAdmin, stored.Role)
}

// TestUserLastAdminGuard — nobody may demote, deactivate or delete the last
// active Admin (409). Since the caller is itself an active Admin, this only
// bites in a race: here the caller is deactivated by someone else after its
// auth state was cached, the way two Admins removing each other would be.
func TestUserLastAdminGuard(t *testing.T) {
	app, db := testutil.App(t)
	caller := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleAdmin)

	// Warm the caller's auth cache, then deactivate it behind the cache's back.
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/users", nil, caller.ID, caller.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
	require.NoError(t, db.Model(caller).Update("is_active", false).Error)

	for name, overrides := range map[string]map[string]string{
		"demote":     {"role": string(models.RoleSalesManager)},
		"deactivate": {"status": "inactive"},
	} {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID), userUpdateBody(target, overrides), caller.ID, caller.Role)
		assert.Equal(t, http.StatusConflict, doJSON(t, app, req, nil).StatusCode, name)
	}
	req = testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(target.ID), nil, caller.ID, caller.Role)
	assert.Equal(t, http.StatusConflict, doJSON(t, app, req, nil).StatusCode, "delete")
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/users/bulk-deactivate", map[string]interface{}{"ids": []uint{target.ID}}, caller.ID, caller.Role)
	assert.Equal(t, http.StatusConflict, doJSON(t, app, req, nil).StatusCode, "bulk")

	var stored models.User
	require.NoError(t, db.First(&stored, target.ID).Error)
	assert.True(t, stored.IsActive)
	assert.Equal(t, models.RoleAdmin, stored.Role)

	// With a second active Admin left, the same demotion goes through.
	require.NoError(t, db.Model(caller).Update("is_active", true).Error)
	req = testutil.AuthRequest(t, http.MethodPut, "/api/v1/users/"+itoa(target.ID),
		userUpdateBody(target, map[string]string{"role": string(models.RoleSalesManager)}), caller.ID, caller.Role)
	assert.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)
}

package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestTaskAssignee_MustBeActiveSalesUser guards validateAssignee on task
// Create, Update, bulk-reassign and campaign task creation: assigned_to must
// be an active user in a sales-pipeline role. Production can't own Tasks
// (§1.7: no access beyond Projects), nor can an inactive or missing user.
func TestTaskAssignee_MustBeActiveSalesUser(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	inactive := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(inactive).Update("is_active", false).Error)
	deal := seedDeal(t, db, nil)
	task := seedTask(t, db, nil)
	campaign := models.Campaign{Name: "Win-back"}
	require.NoError(t, db.Create(&campaign).Error)
	due := time.Now().AddDate(0, 0, 3).Format(time.RFC3339)

	for name, to := range map[string]uint{"production": production.ID, "inactive": inactive.ID, "missing": 99999} {
		var errBody sessionErrBody
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/tasks", map[string]interface{}{
			"related_type": "deal", "related_id": deal.ID, "title": "Call", "due_date": due, "assigned_to": to,
		}, admin.ID, admin.Role)
		require.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, &errBody).StatusCode, "create "+name)
		assert.True(t, errField(errBody, "assigned_to"), name)

		req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/"+itoa(task.ID), map[string]interface{}{
			"title": "Call", "due_date": due, "assigned_to": to,
		}, admin.ID, admin.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "update "+name)

		req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-reassign", map[string]interface{}{
			"ids": []uint{task.ID}, "assigned_to": to,
		}, admin.ID, admin.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "bulk "+name)

		req = testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns/"+itoa(campaign.ID)+"/tasks", map[string]interface{}{
			"targets": []map[string]interface{}{{"related_type": "company", "related_id": deal.CompanyID}},
			"title":   "Call", "due_date": due, "assigned_to": to,
		}, admin.ID, admin.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "campaign "+name)
	}

	var stored models.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	assert.Nil(t, stored.AssignedTo, "a rejected assignee must not be saved")

	// A valid sales user and unassigned both pass.
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/tasks", map[string]interface{}{
		"related_type": "deal", "related_id": deal.ID, "title": "Call", "due_date": due, "assigned_to": rep.ID,
	}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusCreated, doJSON(t, app, req, nil).StatusCode)
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-reassign", map[string]interface{}{
		"ids": []uint{task.ID}, "assigned_to": nil,
	}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusNoContent, doJSON(t, app, req, nil).StatusCode)
}

// TestTaskUpdate_KeepsDeactivatedOwner — Update only checks a changed
// assigned_to, so a task whose owner was since deactivated can still be
// edited without reassigning it (same rule as PUT /deals/:id).
func TestTaskUpdate_KeepsDeactivatedOwner(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	gone := testutil.CreateUser(t, db, models.RoleSalesRep)
	task := seedTask(t, db, &gone.ID)
	require.NoError(t, db.Model(gone).Update("is_active", false).Error)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/"+itoa(task.ID), map[string]interface{}{
		"title": "Renamed", "due_date": task.DueDate.Format(time.RFC3339), "assigned_to": gone.ID,
	}, admin.ID, admin.Role)
	require.Equal(t, http.StatusOK, doJSON(t, app, req, nil).StatusCode)

	var stored models.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	assert.Equal(t, "Renamed", stored.Title)
	require.NotNil(t, stored.AssignedTo)
	assert.Equal(t, gone.ID, *stored.AssignedTo)
}

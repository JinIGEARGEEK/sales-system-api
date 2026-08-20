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

// seedTask creates a bare Task attached to a fresh Deal (related_type "deal"),
// pending by default, optionally pre-assigned.
func seedTask(t *testing.T, db *gorm.DB, assignedTo *uint) *models.Task {
	t.Helper()
	deal := seedDeal(t, db, nil)
	task := &models.Task{
		RelatedType: "deal",
		RelatedID:   deal.ID,
		Title:       "Test Task",
		DueDate:     time.Now().AddDate(0, 0, 7),
		Status:      models.TaskStatusPending,
		AssignedTo:  assignedTo,
	}
	require.NoError(t, db.Create(task).Error)
	return task
}

// TestTasksBulkMarkDone_MarksAllAndAudits guards PATCH /tasks/bulk-mark-done:
// every id transitions to done in one call, and each writes its own
// audit_log_entries row (entity_type=task, action=bulk_marked_done).
func TestTasksBulkMarkDone_MarksAllAndAudits(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	taskA := seedTask(t, db, nil)
	taskB := seedTask(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-mark-done", map[string]interface{}{
		"ids": []uint{taskA.ID, taskB.ID},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var reloadedA, reloadedB models.Task
	require.NoError(t, db.First(&reloadedA, taskA.ID).Error)
	require.NoError(t, db.First(&reloadedB, taskB.ID).Error)
	assert.Equal(t, models.TaskStatusDone, reloadedA.Status)
	assert.Equal(t, models.TaskStatusDone, reloadedB.Status)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND action = ?", "task", "bulk_marked_done").Find(&entries).Error)
	assert.Len(t, entries, 2, "expected one audit entry per marked-done task")
}

// TestTasksBulkMarkDone_RejectsOtherRepsTasks guards the per-row CanWrite
// check: a Sales Rep bulk-marking a task assigned to a different rep must be
// forbidden (and the transaction must roll back — no task ends up done).
func TestTasksBulkMarkDone_RejectsOtherRepsTasks(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	otherRep := testutil.CreateUser(t, db, models.RoleSalesRep)
	ownTask := seedTask(t, db, &rep.ID)
	othersTask := seedTask(t, db, &otherRep.ID)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-mark-done", map[string]interface{}{
		"ids": []uint{ownTask.ID, othersTask.ID},
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	var reloadedOwn models.Task
	require.NoError(t, db.First(&reloadedOwn, ownTask.ID).Error)
	assert.Equal(t, models.TaskStatusPending, reloadedOwn.Status, "the whole bulk call must roll back, not partially apply")
}

// TestTasksBulkMarkDone_RequiresIDs guards the "ids is required" validation.
func TestTasksBulkMarkDone_RequiresIDs(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-mark-done", map[string]interface{}{
		"ids": []uint{},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestTasksBulkReassign_ReassignsAndAudits guards PATCH /tasks/bulk-reassign
// for a Sales Manager reassigning multiple tasks to another rep in one call.
func TestTasksBulkReassign_ReassignsAndAudits(t *testing.T) {
	app, db := testutil.App(t)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	newOwner := testutil.CreateUser(t, db, models.RoleSalesRep)
	taskA := seedTask(t, db, nil)
	taskB := seedTask(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-reassign", map[string]interface{}{
		"ids":         []uint{taskA.ID, taskB.ID},
		"assigned_to": newOwner.ID,
	}, manager.ID, manager.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var reloadedA, reloadedB models.Task
	require.NoError(t, db.First(&reloadedA, taskA.ID).Error)
	require.NoError(t, db.First(&reloadedB, taskB.ID).Error)
	require.NotNil(t, reloadedA.AssignedTo)
	require.NotNil(t, reloadedB.AssignedTo)
	assert.Equal(t, newOwner.ID, *reloadedA.AssignedTo)
	assert.Equal(t, newOwner.ID, *reloadedB.AssignedTo)

	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND action = ?", "task", "bulk_reassigned").Find(&entries).Error)
	assert.Len(t, entries, 2)
}

// TestTasksBulkReassign_RejectsAssigningToAnotherRep guards CanWrite's other
// half: a Sales Rep may bulk-reassign their own tasks to themselves/
// unassigned, but not to a different rep (only a manager/admin can).
func TestTasksBulkReassign_RejectsAssigningToAnotherRep(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	otherRep := testutil.CreateUser(t, db, models.RoleSalesRep)
	ownTask := seedTask(t, db, &rep.ID)

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/tasks/bulk-reassign", map[string]interface{}{
		"ids":         []uint{ownTask.ID},
		"assigned_to": otherRep.ID,
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

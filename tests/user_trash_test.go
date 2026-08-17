package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestUserTrashRestore: DELETE /users/:id soft-deletes (deleted_at), the user
// then disappears from GET /users but shows up in GET /users/trash, and
// POST /users/:id/restore brings it back into the normal list.
func TestUserTrashRestore(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target := testutil.CreateUser(t, db, models.RoleSalesRep)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/users/"+itoa(target.ID), nil, admin.ID, admin.Role)
	resp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	getReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/users/"+itoa(target.ID), nil, admin.ID, admin.Role)
	resp = doJSON(t, app, getReq, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "deleted user must not resolve via GET /users/:id")

	var trash struct {
		Data []models.User `json:"data"`
	}
	trashReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/users/trash", nil, admin.ID, admin.Role)
	resp = doJSON(t, app, trashReq, &trash)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, trash.Data, 1)
	assert.Equal(t, target.ID, trash.Data[0].ID)

	restoreReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/restore", nil, admin.ID, admin.Role)
	resp = doJSON(t, app, restoreReq, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/users/"+itoa(target.ID), nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "restored user must resolve again via GET /users/:id")
}

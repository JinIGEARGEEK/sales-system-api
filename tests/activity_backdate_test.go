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

// TestActivityCreate_BackdatedCreatedAt guards the optional created_at field
// on POST /activities: a past date lets a caller manually log an Activity
// that happened before now (e.g. "mark as contacted on <past date>" from the
// Company page); omitting it keeps the default (GORM's current-time stamp).
func TestActivityCreate_BackdatedCreatedAt(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)

	past := time.Now().AddDate(0, 0, -10).Truncate(time.Second)
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/activities", map[string]interface{}{
		"type": "call", "subject": "call", "related_type": "company", "related_id": company.ID,
		"created_at": past.Format(time.RFC3339),
	}, rep.ID, rep.Role)
	var out struct {
		Data models.Activity `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.WithinDuration(t, past, out.Data.CreatedAt, time.Second)
}

// TestActivityCreate_RejectsFutureCreatedAt guards the validation added
// alongside backdating: a caller can log the past, not schedule a
// not-yet-happened Activity into the future.
func TestActivityCreate_RejectsFutureCreatedAt(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)

	future := time.Now().Add(24 * time.Hour)
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/activities", map[string]interface{}{
		"type": "call", "subject": "call", "related_type": "company", "related_id": company.ID,
		"created_at": future.Format(time.RFC3339),
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestActivityCreate_OmittedCreatedAtDefaultsToNow guards the no-op default:
// a normal Create without created_at must keep stamping the current time,
// unaffected by the new optional field.
func TestActivityCreate_OmittedCreatedAtDefaultsToNow(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)

	before := time.Now()
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/activities", map[string]interface{}{
		"type": "call", "subject": "call", "related_type": "company", "related_id": company.ID,
	}, rep.ID, rep.Role)
	var out struct {
		Data models.Activity `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.WithinDuration(t, before, out.Data.CreatedAt, 5*time.Second)
}

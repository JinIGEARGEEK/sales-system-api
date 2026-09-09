package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestProspectStages_SeededDefaults guards that DefaultProspectStages
// (Marketing's own working-stage list, "Converted" deliberately excluded —
// see ProspectStage's own doc) are seeded and listable via the admin endpoint.
func TestProspectStages_SeededDefaults(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	var out struct {
		Data []models.ProspectStage `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/prospect-stages", nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// Not an exact-length assertion — prospect_stages is a seed-once config
	// table (like prospect_source_options/pipeline_stages), deliberately
	// excluded from TruncateAll between tests, so another test in this
	// package may have already added a row by the time this one runs.
	assert.GreaterOrEqual(t, len(out.Data), len(models.DefaultProspectStages))

	names := make([]string, len(out.Data))
	for i, s := range out.Data {
		names[i] = s.Name
	}
	assert.Contains(t, names, "New")
	assert.Contains(t, names, "Engaging")
	assert.NotContains(t, names, "Converted", `"Converted" is a reserved, system-set stage and must never appear in this table`)

	// The seeded "Disqualified" row must carry IsDisqualifiedStage — frontend
	// code resolves the disqualified-equivalent stage through this flag
	// (mirrors PipelineStage.IsWonStage/IsLostStage) instead of hardcoding the
	// literal name, since an Admin can rename it like any other stage.
	for _, s := range out.Data {
		if s.Name == "Disqualified" {
			assert.True(t, s.IsDisqualifiedStage)
		}
	}
}

// TestProspectStages_ListOpenWritesAdminOnly guards the route-level
// RequireRoles gate — same list-open/writes-admin-only split as every other
// pipeline-config resource (Marketing owns day-to-day Prospect data, not
// this taxonomy's config).
func TestProspectStages_ListOpenWritesAdminOnly(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	t.Run("list is open to marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/prospect-stages", nil, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("create is forbidden for marketing", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/prospect-stages", map[string]interface{}{
			"name": "Warm Lead",
		}, marketing.ID, marketing.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// TestProspectStages_RejectsReservedConvertedName guards that "Converted"
// can never be created or renamed-into via this admin endpoint — it's a
// system-set terminal status (POST /prospects/:id/convert), never a row an
// Admin manages here.
func TestProspectStages_RejectsReservedConvertedName(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/prospect-stages", map[string]interface{}{
		"name": "Converted",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestProspectCreate_RejectsInactiveStage guards that Prospect.status is
// validated against ProspectStage, rejecting a status that doesn't match any
// active row (and isn't the reserved "Converted" literal).
func TestProspectCreate_RejectsInactiveStage(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Riley Chen", "source": "Social Media", "status": "Interested",
	}, marketing.ID, marketing.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestProspectStages_DeactivateThenReject covers the full admin lifecycle:
// create a new stage, use it on a Prospect, deactivate it, then confirm a
// new Prospect can no longer be created with that now-inactive stage.
func TestProspectStages_DeactivateThenReject(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	// prospect_stages is a seed-once config table, deliberately excluded from
	// TruncateAll's per-test wipe (same as prospect_source_options/
	// pipeline_stages) — hard-delete the row this test creates so a rerun
	// against the same (not recreated) test database doesn't collide with
	// the uniqueIndex on Name.
	t.Cleanup(func() {
		db.Unscoped().Where("name = ?", "Warm Lead").Delete(&models.ProspectStage{})
	})

	var created struct {
		Data models.ProspectStage `json:"data"`
	}
	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/prospect-stages", map[string]interface{}{
		"name": "Warm Lead", "sort_order": 4,
	}, admin.ID, admin.Role)
	createResp := doJSON(t, app, createReq, &created)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	assert.True(t, created.Data.IsActive)

	prospectReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Jamie Park", "source": "Social Media", "status": "Warm Lead",
	}, marketing.ID, marketing.Role)
	prospectResp := doJSON(t, app, prospectReq, nil)
	require.Equal(t, http.StatusCreated, prospectResp.StatusCode)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/admin/prospect-stages/"+itoa(created.Data.ID), nil, admin.ID, admin.Role)
	deleteResp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, http.StatusNoContent, deleteResp.StatusCode)

	rejectedReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Alex Kim", "source": "Social Media", "status": "Warm Lead",
	}, marketing.ID, marketing.Role)
	rejectedResp := doJSON(t, app, rejectedReq, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, rejectedResp.StatusCode, "deactivated stage must no longer validate")
}

package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestProspectSources_SeededDefaults guards that DefaultProspectSourceOptions
// (Marketing's own funnel-source list, separate from Lead/Deal's
// LeadSourceOption) are seeded and listable via the admin endpoint.
func TestProspectSources_SeededDefaults(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	var out struct {
		Data []models.ProspectSourceOption `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/prospect-sources", nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// Not an exact-length assertion — prospect_source_options is a
	// seed-once config table (like lead_source_options/pipeline_stages),
	// deliberately excluded from TruncateAll between tests, so another
	// test in this package may have already added a row by the time this
	// one runs. Just confirm the seeded defaults are present.
	assert.GreaterOrEqual(t, len(out.Data), len(models.DefaultProspectSourceOptions))

	names := make([]string, len(out.Data))
	for i, s := range out.Data {
		names[i] = s.Name
	}
	assert.Contains(t, names, "LINE OA")
	assert.Contains(t, names, "Social Media")
}

// TestProspectSources_AdminOnly guards the route-level RequireRoles gate —
// Marketing owns Prospect data day-to-day but not this taxonomy's config,
// same convention as every other /admin/* option list in this app.
func TestProspectSources_AdminOnly(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/prospect-sources", nil, marketing.ID, marketing.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestProspectCreate_RejectsInactiveSource guards that Prospect.source is
// validated against ProspectSourceOption, not LeadSourceOption — a valid
// Lead source ("Website") must NOT be accepted here since the two lists are
// deliberately separate (see ProspectSource's doc comment).
func TestProspectCreate_RejectsInactiveSource(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Riley Chen", "source": "Website",
	}, marketing.ID, marketing.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestProspectSources_DeactivateThenReject covers the full admin lifecycle:
// create a new source, use it on a Prospect, deactivate it, then confirm a
// new Prospect can no longer be created with that now-inactive source.
func TestProspectSources_DeactivateThenReject(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	// prospect_source_options is a seed-once config table, deliberately
	// excluded from TruncateAll's per-test wipe (same as lead_source_options/
	// pipeline_stages) — hard-delete the row this test creates so a rerun
	// against the same (not recreated) test database doesn't collide with
	// the uniqueIndex on Name.
	t.Cleanup(func() {
		db.Unscoped().Where("name = ?", "Trade Show").Delete(&models.ProspectSourceOption{})
	})

	var created struct {
		Data models.ProspectSourceOption `json:"data"`
	}
	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/prospect-sources", map[string]interface{}{
		"name": "Trade Show",
	}, admin.ID, admin.Role)
	createResp := doJSON(t, app, createReq, &created)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	assert.True(t, created.Data.IsActive)

	prospectReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Jamie Park", "source": "Trade Show",
	}, marketing.ID, marketing.Role)
	prospectResp := doJSON(t, app, prospectReq, nil)
	require.Equal(t, http.StatusCreated, prospectResp.StatusCode)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/admin/prospect-sources/"+itoa(created.Data.ID), nil, admin.ID, admin.Role)
	deleteResp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, http.StatusNoContent, deleteResp.StatusCode)

	rejectedReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "Alex Kim", "source": "Trade Show",
	}, marketing.ID, marketing.Role)
	rejectedResp := doJSON(t, app, rejectedReq, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, rejectedResp.StatusCode, "deactivated source must no longer validate")
}

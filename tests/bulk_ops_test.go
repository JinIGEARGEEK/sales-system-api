package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// TestBulkReassign_DealLeadProspect guards bulkReassignEntity (shared by
// Deal/Lead/Prospect's own BulkReassign — see bulk_ops.go) across all three
// resources: reassigning to a new owner in one call.
func TestBulkReassign_DealLeadProspect(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	deal := seedDeal(t, db, nil)
	lead := seedLead(t, db, nil)
	prospect := seedProspect(t, db, nil)

	cases := []struct {
		name string
		path string
		id   uint
	}{
		{"deal", "/api/v1/deals/bulk-reassign", deal.ID},
		{"lead", "/api/v1/leads/bulk-reassign", lead.ID},
		{"prospect", "/api/v1/prospects/bulk-reassign", prospect.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPatch, tc.path,
				map[string]interface{}{"ids": []uint{tc.id}, "assigned_to": rep.ID}, admin.ID, models.RoleAdmin)
			resp := doJSON(t, app, req, nil)
			require.Equal(t, fiber.StatusNoContent, resp.StatusCode)
		})
	}

	var gotDeal models.Deal
	require.NoError(t, db.First(&gotDeal, deal.ID).Error)
	require.NotNil(t, gotDeal.AssignedTo)
	require.Equal(t, rep.ID, *gotDeal.AssignedTo)

	var gotLead models.Lead
	require.NoError(t, db.First(&gotLead, lead.ID).Error)
	require.NotNil(t, gotLead.AssignedTo)
	require.Equal(t, rep.ID, *gotLead.AssignedTo)

	var gotProspect models.Prospect
	require.NoError(t, db.First(&gotProspect, prospect.ID).Error)
	require.NotNil(t, gotProspect.AssignedTo)
	require.Equal(t, rep.ID, *gotProspect.AssignedTo)
}

// TestBulkTag_DealLeadProspect guards bulkTagEntity's "add" (merge, default)
// and "set" (replace) modes across all three resources.
func TestBulkTag_DealLeadProspect(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(deal).Update("tags", pq.StringArray{"existing"}).Error)

	// "add" mode merges into the Deal's existing tags.
	addReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/bulk-tag",
		map[string]interface{}{"ids": []uint{deal.ID}, "tags": []string{"vip"}, "mode": "add"}, admin.ID, models.RoleAdmin)
	addResp := doJSON(t, app, addReq, nil)
	require.Equal(t, fiber.StatusNoContent, addResp.StatusCode)
	var afterAdd models.Deal
	require.NoError(t, db.First(&afterAdd, deal.ID).Error)
	require.ElementsMatch(t, []string{"existing", "vip"}, []string(afterAdd.Tags))

	// "set" mode replaces them outright.
	setReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/bulk-tag",
		map[string]interface{}{"ids": []uint{deal.ID}, "tags": []string{"only-this"}, "mode": "set"}, admin.ID, models.RoleAdmin)
	setResp := doJSON(t, app, setReq, nil)
	require.Equal(t, fiber.StatusNoContent, setResp.StatusCode)
	var afterSet models.Deal
	require.NoError(t, db.First(&afterSet, deal.ID).Error)
	require.Equal(t, []string{"only-this"}, []string(afterSet.Tags))

	// Lead and Prospect get the same "set" treatment, once each, as a
	// lighter-weight cross-resource check (the merge-vs-set logic itself is
	// exercised in full against Deal above).
	lead := seedLead(t, db, nil)
	leadReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/bulk-tag",
		map[string]interface{}{"ids": []uint{lead.ID}, "tags": []string{"hot"}, "mode": "set"}, admin.ID, models.RoleAdmin)
	leadResp := doJSON(t, app, leadReq, nil)
	require.Equal(t, fiber.StatusNoContent, leadResp.StatusCode)
	var gotLead models.Lead
	require.NoError(t, db.First(&gotLead, lead.ID).Error)
	require.Equal(t, []string{"hot"}, []string(gotLead.Tags))

	prospect := seedProspect(t, db, nil)
	prospectReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/prospects/bulk-tag",
		map[string]interface{}{"ids": []uint{prospect.ID}, "tags": []string{"warm"}, "mode": "set"}, admin.ID, models.RoleAdmin)
	prospectResp := doJSON(t, app, prospectReq, nil)
	require.Equal(t, fiber.StatusNoContent, prospectResp.StatusCode)
	var gotProspect models.Prospect
	require.NoError(t, db.First(&gotProspect, prospect.ID).Error)
	require.Equal(t, []string{"warm"}, []string(gotProspect.Tags))
}

// TestBulkArchive_DealLeadProspect guards bulkArchiveEntity's soft-delete
// across all three resources — same effect as each one's own single-record
// Delete, in one transaction.
func TestBulkArchive_DealLeadProspect(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	deal := seedDeal(t, db, nil)
	lead := seedLead(t, db, nil)
	prospect := seedProspect(t, db, nil)

	cases := []struct {
		name string
		path string
		id   uint
	}{
		{"deal", "/api/v1/deals/bulk-archive", deal.ID},
		{"lead", "/api/v1/leads/bulk-archive", lead.ID},
		{"prospect", "/api/v1/prospects/bulk-archive", prospect.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPatch, tc.path,
				map[string]interface{}{"ids": []uint{tc.id}}, admin.ID, models.RoleAdmin)
			resp := doJSON(t, app, req, nil)
			require.Equal(t, fiber.StatusNoContent, resp.StatusCode)
		})
	}

	var dealCount, leadCount, prospectCount int64
	db.Model(&models.Deal{}).Where("id = ?", deal.ID).Count(&dealCount)
	db.Model(&models.Lead{}).Where("id = ?", lead.ID).Count(&leadCount)
	db.Model(&models.Prospect{}).Where("id = ?", prospect.ID).Count(&prospectCount)
	require.Zero(t, dealCount, "soft-deleted Deal must be excluded from the default scope")
	require.Zero(t, leadCount, "soft-deleted Lead must be excluded from the default scope")
	require.Zero(t, prospectCount, "soft-deleted Prospect must be excluded from the default scope")
}

// TestBulkOps_RequireIDs guards the "ids is required" validation shared by
// all three bulk endpoints on all three resources — a quick sweep rather
// than repeating the full body for every combination.
func TestBulkOps_RequireIDs(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	paths := []string{
		"/api/v1/deals/bulk-reassign", "/api/v1/deals/bulk-tag", "/api/v1/deals/bulk-archive",
		"/api/v1/leads/bulk-reassign", "/api/v1/leads/bulk-tag", "/api/v1/leads/bulk-archive",
		"/api/v1/prospects/bulk-reassign", "/api/v1/prospects/bulk-tag", "/api/v1/prospects/bulk-archive",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPatch, path, map[string]interface{}{}, admin.ID, models.RoleAdmin)
			resp := doJSON(t, app, req, nil)
			require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
		})
	}
}

// TestBulkOps_RejectsTooManyIDs guards utils.MaxBulkIDs: a request with more
// ids than the cap must be rejected with a 422 rather than opening a
// transaction across an unbounded number of row locks.
func TestBulkOps_RejectsTooManyIDs(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	tooMany := make([]uint, utils.MaxBulkIDs+1)
	for i := range tooMany {
		tooMany[i] = uint(i + 1)
	}

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/bulk-archive",
		map[string]interface{}{"ids": tooMany}, admin.ID, models.RoleAdmin)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
}

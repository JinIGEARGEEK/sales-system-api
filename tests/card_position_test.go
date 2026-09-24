package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/handlers"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

func setPosition(t *testing.T, db *gorm.DB, model interface{}, pos float64) {
	t.Helper()
	require.NoError(t, db.Model(model).UpdateColumn("position", pos).Error)
}

func dealPosition(t *testing.T, db *gorm.DB, id uint) float64 {
	t.Helper()
	var d models.Deal
	require.NoError(t, db.First(&d, id).Error)
	return d.Position
}

func leadPosition(t *testing.T, db *gorm.DB, id uint) float64 {
	t.Helper()
	var l models.Lead
	require.NoError(t, db.First(&l, id).Error)
	return l.Position
}

// TestCardPosition_CreateAppendsToLane: each new card lands at the end of its
// own lane only.
func TestCardPosition_CreateAppendsToLane(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	create := func(stage string) models.Deal {
		var out struct {
			Data models.Deal `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
			"company_id": company.ID, "contact_id": contact.ID, "title": "Deal", "value": 100, "stage": stage,
		}, admin.ID, admin.Role)
		require.Equal(t, fiber.StatusCreated, doJSON(t, app, req, &out).StatusCode)
		return out.Data
	}
	assert.Equal(t, 1.0, create("Lead").Position)
	assert.Equal(t, 2.0, create("Lead").Position)
	assert.Equal(t, 1.0, create("Qualified").Position, "positions are per lane")
}

// TestCardPosition_PatchStage covers the drag-move endpoint: an explicit
// position is stored, an omitted one appends on a lane change and is left
// alone on a same-lane save, and an out-of-range one is rejected.
func TestCardPosition_PatchStage(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	a, b := seedDeal(t, db, nil), seedDeal(t, db, nil)
	setPosition(t, db, a, 1)
	setPosition(t, db, b, 2)
	q := seedDeal(t, db, nil)
	require.NoError(t, db.Model(q).UpdateColumns(map[string]interface{}{"stage": "Qualified", "position": 7}).Error)

	patch := func(id uint, body map[string]interface{}) *http.Response {
		req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(id)+"/stage", body, admin.ID, admin.Role)
		return doJSON(t, app, req, nil)
	}

	require.Equal(t, fiber.StatusOK, patch(b.ID, map[string]interface{}{"stage": "Lead", "position": 0.5}).StatusCode)
	assert.Equal(t, 0.5, dealPosition(t, db, b.ID), "explicit position is stored")

	require.Equal(t, fiber.StatusOK, patch(b.ID, map[string]interface{}{"stage": "Lead"}).StatusCode)
	assert.Equal(t, 0.5, dealPosition(t, db, b.ID), "same-lane save without a position keeps it")

	require.Equal(t, fiber.StatusOK, patch(a.ID, map[string]interface{}{"stage": "Qualified"}).StatusCode)
	assert.Equal(t, 8.0, dealPosition(t, db, a.ID), "lane change without a position appends")

	resp := patch(b.ID, map[string]interface{}{"stage": "Lead", "position": 1e12})
	assert.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, 0.5, dealPosition(t, db, b.ID), "rejected move changes nothing")
}

// TestCardPosition_RebalancesCrowdedLane: dropping a card too close to a
// neighbor renumbers the lane to 1..n in its current order, and tells the
// client via the rebalance header.
func TestCardPosition_RebalancesCrowdedLane(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	first, second, moved := seedLead(t, db, nil), seedLead(t, db, nil), seedLead(t, db, nil)
	setPosition(t, db, first, 1)
	setPosition(t, db, second, 1+1e-7)
	setPosition(t, db, moved, 5)

	var out struct {
		Data models.Lead `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/"+itoa(moved.ID)+"/status",
		map[string]interface{}{"status": string(moved.Status), "position": 1 + 5e-8}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "true", resp.Header.Get(handlers.LaneRebalancedHeader))

	assert.Equal(t, 1.0, leadPosition(t, db, first.ID))
	assert.Equal(t, 2.0, leadPosition(t, db, moved.ID), "moved card keeps its dropped-between spot")
	assert.Equal(t, 3.0, leadPosition(t, db, second.ID))
	assert.Equal(t, 2.0, out.Data.Position, "response carries the renumbered position")

	// A roomy drop doesn't rebalance.
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/"+itoa(moved.ID)+"/status",
		map[string]interface{}{"status": string(moved.Status), "position": 2.5}, admin.ID, admin.Role)
	resp = doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get(handlers.LaneRebalancedHeader))
}

// TestCardPosition_PutLaneChangeAppends: a status change through the full
// edit form lands at the end of the new lane, not at the old lane's value.
func TestCardPosition_PutLaneChangeAppends(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	lead, other := seedLead(t, db, nil), seedLead(t, db, nil)
	setPosition(t, db, lead, 1)
	require.NoError(t, db.Model(other).UpdateColumns(map[string]interface{}{"status": "Contacted", "position": 4}).Error)

	put := func(status string) {
		req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
			"name": lead.Name, "source": string(lead.Source), "status": status,
		}, admin.ID, admin.Role)
		require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	}
	put(string(lead.Status))
	assert.Equal(t, 1.0, leadPosition(t, db, lead.ID), "same-lane edit keeps position")
	put("Contacted")
	assert.Equal(t, 5.0, leadPosition(t, db, lead.ID))
}

// TestCardPosition_ConversionsAppend: records created or moved by Lead→Deal
// and Prospect→Lead conversion get a real end-of-lane position, not 0.
func TestCardPosition_ConversionsAppend(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	existing := seedDeal(t, db, nil)
	setPosition(t, db, existing, 3)
	qualified := seedLead(t, db, nil)
	setPosition(t, db, qualified, 6)

	company := seedCompany(t, db)
	lead := &models.Lead{Name: "Jordan Lee", CompanyID: &company.ID, Source: models.LeadSourceWebsite, Status: models.LeadStatusContacted}
	require.NoError(t, db.Create(lead).Error)
	var dealOut struct {
		Data struct {
			Deal models.Deal `json:"deal"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"deal": map[string]interface{}{"title": "Acme Deal", "value": 5000, "stage": "Lead"},
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &dealOut).StatusCode)
	assert.Equal(t, 4.0, dealPosition(t, db, dealOut.Data.Deal.ID), "new Deal appends to its stage lane")
	assert.Equal(t, 7.0, leadPosition(t, db, lead.ID), "converted Lead appends to the Qualified lane")

	prospect := seedProspect(t, db, &company.ID)
	var leadOut struct {
		Data struct {
			Lead models.Lead `json:"lead"`
		} `json:"data"`
	}
	req = testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert", map[string]interface{}{}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &leadOut).StatusCode)
	assert.Equal(t, 1.0, leadPosition(t, db, leadOut.Data.Lead.ID), "new Lead appends to the (empty) New lane")
	var reloaded models.Prospect
	require.NoError(t, db.First(&reloaded, prospect.ID).Error)
	assert.Equal(t, 1.0, reloaded.Position, "converted Prospect appends to the Converted lane")
}

// TestBackfillCardPositions: rows at 0 are appended after the lane's existing
// cards in created_at order, others are untouched, and it runs only once — a
// later card legitimately dragged to 0 is never renumbered on a restart.
func TestBackfillCardPositions(t *testing.T) {
	_, db := testutil.App(t)
	placed, zeroA, zeroB := seedDeal(t, db, nil), seedDeal(t, db, nil), seedDeal(t, db, nil)
	setPosition(t, db, placed, 2.5)
	other := seedDeal(t, db, nil)
	require.NoError(t, db.Model(other).UpdateColumn("stage", "Qualified").Error)

	require.NoError(t, database.BackfillCardPositions(db))
	assert.Equal(t, 2.5, dealPosition(t, db, placed.ID))
	assert.Equal(t, 3.5, dealPosition(t, db, zeroA.ID))
	assert.Equal(t, 4.5, dealPosition(t, db, zeroB.ID))
	assert.Equal(t, 1.0, dealPosition(t, db, other.ID), "each lane numbers on its own")

	setPosition(t, db, zeroA, 0)
	require.NoError(t, database.BackfillCardPositions(db))
	assert.Equal(t, 0.0, dealPosition(t, db, zeroA.ID), "second run is a no-op")
}

// TestCardPosition_SortTiebreak: cards sharing a position come back in a
// stable id order, in the requested direction.
func TestCardPosition_SortTiebreak(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	var ids []uint
	for i := 0; i < 3; i++ {
		l := seedLead(t, db, nil)
		setPosition(t, db, l, 1)
		ids = append(ids, l.ID)
	}
	list := func(sort string) []uint {
		var out struct {
			Data []models.Lead `json:"data"`
		}
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/leads?sort="+sort, nil, admin.ID, admin.Role)
		require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &out).StatusCode)
		got := make([]uint, 0, len(out.Data))
		for _, l := range out.Data {
			got = append(got, l.ID)
		}
		return got
	}
	assert.Equal(t, ids, list("position"))
	assert.Equal(t, []uint{ids[2], ids[1], ids[0]}, list("-position"))
}

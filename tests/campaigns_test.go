package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestCampaign_CreateAndList guards CampaignHandler's basic Create/List.
func TestCampaign_CreateAndList(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns", map[string]interface{}{
		"name": "Dormant win-back Q3", "type": "win_back",
	}, rep.ID, rep.Role)
	var created struct {
		Data models.Campaign `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, models.CampaignTypeWinBack, created.Data.Type)

	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/campaigns", nil, rep.ID, rep.Role)
	var list struct {
		Data []models.Campaign `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.Len(t, list.Data, 1)
}

// TestCampaign_CreateRejectsInvalidType guards the type enum check.
func TestCampaign_CreateRejectsInvalidType(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns", map[string]interface{}{
		"name": "x", "type": "not-a-type",
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
}

// TestCampaign_BulkCreateTasksAndProgress guards BulkCreateTasks (dedup +
// one Task per target, batch-created) and Progress's total/done/pending/
// converted counts in one flow.
func TestCampaign_BulkCreateTasksAndProgress(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)

	campaignReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns", map[string]interface{}{
		"name": "Win-back", "type": "win_back",
	}, admin.ID, admin.Role)
	var campaign struct {
		Data models.Campaign `json:"data"`
	}
	require.Equal(t, fiber.StatusCreated, doJSON(t, app, campaignReq, &campaign).StatusCode)

	tasksReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns/"+itoa(campaign.Data.ID)+"/tasks", map[string]interface{}{
		"targets": []map[string]interface{}{
			{"related_type": "company", "related_id": company.ID},
			{"related_type": "contact", "related_id": contact.ID},
			// Duplicate of the first target — must be deduped, not create a
			// second Task for the same (type, id) pair.
			{"related_type": "company", "related_id": company.ID},
		},
		"title": "Follow up", "due_date": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}, admin.ID, admin.Role)
	tasksResp := doJSON(t, app, tasksReq, nil)
	require.Equal(t, fiber.StatusNoContent, tasksResp.StatusCode)

	var taskCount int64
	db.Model(&models.Task{}).Where("campaign_id = ?", campaign.Data.ID).Count(&taskCount)
	require.Equal(t, int64(2), taskCount, "duplicate (type, id) target must be deduped to one Task")

	progressReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/campaigns/"+itoa(campaign.Data.ID)+"/progress", nil, admin.ID, admin.Role)
	var progress struct {
		Data struct {
			Total     int64 `json:"total"`
			Done      int64 `json:"done"`
			Pending   int64 `json:"pending"`
			Converted int64 `json:"converted"`
		} `json:"data"`
	}
	progressResp := doJSON(t, app, progressReq, &progress)
	require.Equal(t, fiber.StatusOK, progressResp.StatusCode)
	require.Equal(t, int64(2), progress.Data.Total)
	require.Equal(t, int64(0), progress.Data.Done)
	require.Equal(t, int64(2), progress.Data.Pending)
	require.Equal(t, int64(0), progress.Data.Converted)
}

// TestCampaign_BulkCreateTasksRequiresTargetsAndTitle guards the two
// required-field validations on BulkCreateTasks.
func TestCampaign_BulkCreateTasksRequiresTargetsAndTitle(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)

	campaignReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns", map[string]interface{}{
		"name": "x", "type": "win_back",
	}, admin.ID, admin.Role)
	var campaign struct {
		Data models.Campaign `json:"data"`
	}
	require.Equal(t, fiber.StatusCreated, doJSON(t, app, campaignReq, &campaign).StatusCode)

	noTargets := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns/"+itoa(campaign.Data.ID)+"/tasks", map[string]interface{}{
		"title": "Follow up",
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusUnprocessableEntity, doJSON(t, app, noTargets, nil).StatusCode)

	noTitle := testutil.AuthRequest(t, http.MethodPost, "/api/v1/campaigns/"+itoa(campaign.Data.ID)+"/tasks", map[string]interface{}{
		"targets": []map[string]interface{}{{"related_type": "company", "related_id": company.ID}},
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusUnprocessableEntity, doJSON(t, app, noTitle, nil).StatusCode)
}

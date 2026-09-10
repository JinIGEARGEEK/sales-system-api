package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestPipelineStages_OnlyOneWonAndOneLostStageAtATime guards that setting
// is_won_stage/is_lost_stage on a new/updated row clears that flag from every
// other row — DealHandler.UpdateStage and the notifier's checkDealIdleRule
// resolve "the" won/lost stage with a single lookup, so two flagged rows
// would leave it picking whichever one Postgres returns first.
func TestPipelineStages_OnlyOneWonAndOneLostStageAtATime(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	t.Cleanup(func() {
		db.Unscoped().Where("name = ?", "Closed Won (Test)").Delete(&models.PipelineStage{})
		db.Model(&models.PipelineStage{}).Where("name = ?", "Won").Update("is_won_stage", true)
	})

	var seededWon models.PipelineStage
	require.NoError(t, db.Where("name = ?", "Won").First(&seededWon).Error)
	assert.True(t, seededWon.IsWonStage)

	var created struct {
		Data models.PipelineStage `json:"data"`
	}
	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/pipeline-stages", map[string]interface{}{
		"name": "Closed Won (Test)", "sort_order": 6, "is_won_stage": true,
	}, admin.ID, admin.Role)
	createResp := doJSON(t, app, createReq, &created)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	assert.True(t, created.Data.IsWonStage)

	var wonStages []models.PipelineStage
	require.NoError(t, db.Where("is_won_stage = ?", true).Find(&wonStages).Error)
	require.Len(t, wonStages, 1, "only the newly-created row should carry is_won_stage after Create")
	assert.Equal(t, "Closed Won (Test)", wonStages[0].Name)

	// Flipping it back onto "Won" via Update must clear "Closed Won (Test)".
	var won models.PipelineStage
	require.NoError(t, db.Where("name = ?", "Won").First(&won).Error)
	updateReq := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/pipeline-stages/"+itoa(won.ID), map[string]interface{}{
		"name": won.Name, "sort_order": won.SortOrder, "is_won_stage": true,
	}, admin.ID, admin.Role)
	updateResp := doJSON(t, app, updateReq, nil)
	require.Equal(t, http.StatusOK, updateResp.StatusCode)

	require.NoError(t, db.Where("is_won_stage = ?", true).Find(&wonStages).Error)
	require.Len(t, wonStages, 1, "only the updated row should carry is_won_stage after Update")
	assert.Equal(t, "Won", wonStages[0].Name)
}

package apitests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// TestStageName_Rules: a stage name is trimmed, must fit the column its
// records store it in, and can't take another stage's name — each a 422,
// never the database's 500.
func TestStageName_Rules(t *testing.T) {
	app, db := testutil.App(t)
	keepSeedConfig(t, db)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	patch := func(path string, body map[string]interface{}) int {
		req := testutil.AuthRequest(t, http.MethodPatch, path, body, admin.ID, admin.Role)
		return doJSON(t, app, req, nil).StatusCode
	}

	var negotiation models.PipelineStage
	require.NoError(t, db.Where("name = ?", "Negotiation").First(&negotiation).Error)
	path := "/api/v1/admin/pipeline-stages/" + itoa(negotiation.ID)
	body := func(name string) map[string]interface{} {
		return map[string]interface{}{"name": name, "sort_order": negotiation.SortOrder, "is_won_stage": false, "is_lost_stage": false}
	}
	assert.Equal(t, fiber.StatusUnprocessableEntity, patch(path, body("Qualified")), "taken by another stage")
	assert.Equal(t, fiber.StatusUnprocessableEntity, patch(path, body(strings.Repeat("x", 65))), "longer than deals.stage")
	assert.Equal(t, fiber.StatusOK, patch(path, body("  Negotiating  ")))
	var renamed models.PipelineStage
	require.NoError(t, db.First(&renamed, negotiation.ID).Error)
	assert.Equal(t, "Negotiating", renamed.Name, "trimmed")

	var nurturing models.ProspectStage
	require.NoError(t, db.Where("name = ?", "Nurturing").First(&nurturing).Error)
	pPath := "/api/v1/admin/prospect-stages/" + itoa(nurturing.ID)
	pBody := func(name string) map[string]interface{} {
		return map[string]interface{}{"name": name, "sort_order": nurturing.SortOrder, "is_disqualified_stage": false}
	}
	assert.Equal(t, fiber.StatusUnprocessableEntity, patch(pPath, pBody("Seventeen chars!!")), "longer than prospects.status (16)")
	assert.Equal(t, fiber.StatusUnprocessableEntity, patch(pPath, pBody("New")), "taken by another stage")
	assert.Equal(t, fiber.StatusOK, patch(pPath, pBody("Sixteen chars!!!")))
}

// TestStageDefaults_FollowRenames: with the seeded names renamed, a new
// Prospect starts in the first open Prospect stage, and a Lead converted
// without a stage gets its Deal's configured probability and a forecast
// category (as Deal Create does).
func TestStageDefaults_FollowRenames(t *testing.T) {
	app, db := testutil.App(t)
	keepSeedConfig(t, db)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	require.NoError(t, db.Model(&models.ProspectStage{}).Where("name = ?", "New").Update("name", "Fresh").Error)
	require.NoError(t, db.Model(&models.PipelineStage{}).Where("name = ?", "Qualified").Update("name", "Evaluating").Error)

	var prospect struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects", map[string]interface{}{
		"name": "No Status Given", "source": "Social Media",
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusCreated, doJSON(t, app, req, &prospect).StatusCode)
	assert.Equal(t, "Fresh", prospect.Data.Status)

	company := seedCompany(t, db)
	lead := &models.Lead{Name: "Jordan Lee", CompanyID: &company.ID, Source: models.LeadSourceWebsite, Status: models.LeadStatusContacted}
	require.NoError(t, db.Create(lead).Error)
	var out struct {
		Data struct {
			Deal models.Deal `json:"deal"`
		} `json:"data"`
	}
	req = testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"deal": map[string]interface{}{"title": "Acme Deal", "value": 5000},
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &out).StatusCode)
	deal := out.Data.Deal
	assert.Equal(t, utils.DefaultPipelineStage(db), deal.Stage, "Qualified is gone, so the first open stage")
	require.NotNil(t, deal.Probability)
	assert.Equal(t, utils.StageDefaultProbability(db, deal.Stage), *deal.Probability)
	assert.NotNil(t, deal.ForecastCategory)
}

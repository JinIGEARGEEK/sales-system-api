package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDealStageChange_LogsCompanyActivity guards the fix making
// Company.last_activity_at reflect a Deal stage change: a stage move via
// either PATCH /deals/:id/stage or the Overview PUT must bump the parent
// Company's last_activity_at, not just write the stage_changed audit entry.
func TestDealStageChange_LogsCompanyActivity(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, &rep.ID)

	var countBefore int64
	require.NoError(t, db.Model(&models.Activity{}).
		Where("related_type = ? AND related_id = ?", models.RelatedTypeCompany, deal.CompanyID).Count(&countBefore).Error)
	require.Zero(t, countBefore, "no Activity yet")

	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage",
		map[string]interface{}{"stage": string(models.DealStageQualified)}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var activity models.Activity
	require.NoError(t, db.Where("related_type = ? AND related_id = ?", models.RelatedTypeCompany, deal.CompanyID).
		First(&activity).Error)
	assert.Contains(t, activity.Subject, "Deal stage changed")
}

// TestLeadStatusChange_LogsCompanyActivity mirrors the Deal case for Lead
// Update — same company-scoped Activity, gated on Status actually changing.
func TestLeadStatusChange_LogsCompanyActivity(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)
	lead := seedLead(t, db, &company.ID)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
		"name": lead.Name, "source": string(lead.Source), "status": "New",
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var activity models.Activity
	require.NoError(t, db.Where("related_type = ? AND related_id = ?", models.RelatedTypeCompany, company.ID).
		First(&activity).Error, "a Lead status change must log a company-scoped Activity")
	assert.Contains(t, activity.Subject, "Lead status changed")
}

// TestLeadUpdate_NoStatusChangeDoesNotLogActivity guards the gate itself:
// resubmitting the same status (a plain "edit this record" save with no
// actual transition) must not fabricate contact history.
func TestLeadUpdate_NoStatusChangeDoesNotLogActivity(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)
	lead := seedLead(t, db, &company.ID)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID), map[string]interface{}{
		"name": lead.Name, "source": string(lead.Source), "status": string(lead.Status),
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var count int64
	require.NoError(t, db.Model(&models.Activity{}).
		Where("related_type = ? AND related_id = ?", models.RelatedTypeCompany, company.ID).Count(&count).Error)
	assert.Zero(t, count, "no status transition means no Activity should be logged")
}

// TestProspectStatusChange_LogsCompanyActivity mirrors the same behavior for
// Prospect Update.
func TestProspectStatusChange_LogsCompanyActivity(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)
	prospect := seedProspect(t, db, &company.ID)

	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/prospects/"+itoa(prospect.ID), map[string]interface{}{
		"name": prospect.Name, "source": prospect.Source, "status": "Nurturing",
	}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var activity models.Activity
	require.NoError(t, db.Where("related_type = ? AND related_id = ?", models.RelatedTypeCompany, company.ID).
		First(&activity).Error, "a Prospect status change must log a company-scoped Activity")
	assert.Contains(t, activity.Subject, "Prospect status changed")
}

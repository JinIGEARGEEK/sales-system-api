package apitests

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/digest"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// Weekly Overview Pipeline digest (FR-CRM-123, 2026-09-24).

type capturedMail struct{ to, subject, body string }

func captureDigestMail(t *testing.T) *[]capturedMail {
	t.Helper()
	var sent []capturedMail
	orig := digest.SendMail
	digest.SendMail = func(_ *config.Config, to, subject, body string) error {
		sent = append(sent, capturedMail{to, subject, body})
		return nil
	}
	t.Cleanup(func() { digest.SendMail = orig })
	return &sent
}

// A Wednesday; its week's Monday is 2026-09-21, so the digest covers
// Mon 14 – Sun 20 Sep.
var digestNow = time.Date(2026, 9, 23, 10, 0, 0, 0, time.Local)

// TestWeeklyDigest_Content: last week's summary, stale/slipped/lost lists
// with owners and reasons, and the recipients (active Admins/Sales Managers).
func TestWeeklyDigest_Content(t *testing.T) {
	_, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	inactive := testutil.CreateUser(t, db, models.RoleAdmin)
	require.NoError(t, db.Model(inactive).Update("is_active", false).Error)

	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	lastWeek := time.Date(2026, 9, 16, 11, 0, 0, 0, time.Local)
	longAgo := time.Date(2026, 8, 1, 9, 0, 0, 0, time.Local)
	prev := "Negotiation"
	competitor := models.LostReasonCompetitor
	for _, d := range []*models.Deal{
		{Title: "Waiting Forever", Value: 850000, Stage: models.DealStageQualified, Status: models.DealStatusOpen, StageEnteredAt: &longAgo, AssignedTo: &rep.ID},
		{Title: "Slipped Deal", Value: 300000, Stage: models.DealStageQualified, Status: models.DealStatusOpen, StageEnteredAt: &lastWeek, PreviousStage: &prev},
		{Title: "Lost Deal", Value: 900000, Stage: models.DealStageLost, Status: models.DealStatusLost, StageEnteredAt: &lastWeek, LostReason: &competitor},
	} {
		d.CompanyID, d.ContactID = company.ID, contact.ID
		require.NoError(t, db.Create(d).Error)
		require.NoError(t, db.Model(d).UpdateColumn("created_at", longAgo).Error)
	}

	w, err := digest.BuildWeekly(db, &config.Config{AppURL: "https://crm.example.com/"}, digestNow)
	require.NoError(t, err)
	assert.Equal(t, "Weekly pipeline review: 14 Sep - 20 Sep 2026", w.Subject)
	assert.ElementsMatch(t, []string{admin.Email, manager.Email}, w.Recipients, "active Admins and Sales Managers only")
	for _, want := range []string{
		"SUMMARY", "NEEDS ATTENTION",
		"Waiting Forever (Acme Corp): Qualified for", // stale, longest first
		rep.FirstName,
		"Slipped Deal (Acme Corp): Negotiation -> Qualified",
		"Lost Deal (Acme Corp): ฿900K, Competitor",
		"https://crm.example.com/crm/overview-pipeline?period=lastWeek",
	} {
		assert.Contains(t, w.Body, want)
	}
}

// TestWeeklyDigest_Schedule: due from Monday 08:00, sent once per week,
// and never when disabled or without SMTP.
func TestWeeklyDigest_Schedule(t *testing.T) {
	_, db := testutil.App(t)
	keepSeedConfig(t, db)
	testutil.CreateUser(t, db, models.RoleAdmin)
	sent := captureDigestMail(t)
	cfg := &config.Config{SMTPHost: "smtp.example.com"}
	monday := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)

	require.NoError(t, digest.MaybeSendWeekly(db, &config.Config{}, monday.Add(9*time.Hour)))
	assert.Empty(t, *sent, "no SMTP, no send")

	require.NoError(t, digest.MaybeSendWeekly(db, cfg, monday.Add(7*time.Hour)))
	assert.Empty(t, *sent, "not before 08:00 Monday")

	require.NoError(t, digest.MaybeSendWeekly(db, cfg, monday.Add(8*time.Hour)))
	require.Len(t, *sent, 1)

	require.NoError(t, digest.MaybeSendWeekly(db, cfg, monday.Add(30*time.Hour)))
	assert.Len(t, *sent, 1, "once per week, even across restarts")

	nextMonday := monday.AddDate(0, 0, 7).Add(9 * time.Hour)
	require.NoError(t, db.Model(&models.AppSettings{}).Where("id = ?", 1).Update("weekly_digest_enabled", false).Error)
	require.NoError(t, digest.MaybeSendWeekly(db, cfg, nextMonday))
	assert.Len(t, *sent, 1, "disabled")

	require.NoError(t, db.Model(&models.AppSettings{}).Where("id = ?", 1).Update("weekly_digest_enabled", true).Error)
	require.NoError(t, digest.MaybeSendWeekly(db, cfg, nextMonday))
	assert.Len(t, *sent, 2, "next week sends again")
}

// TestWeeklyDigest_SlippedInBusyLane: a Deal that slipped back last week
// is listed even when its lane holds more longer-waiting Deals than the
// email lists (open lanes rank longest-waiting first).
func TestWeeklyDigest_SlippedInBusyLane(t *testing.T) {
	_, db := testutil.App(t)
	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	longAgo := time.Date(2026, 8, 1, 9, 0, 0, 0, time.Local)
	lastWeek := time.Date(2026, 9, 16, 11, 0, 0, 0, time.Local)
	prev := "Negotiation"
	deals := []*models.Deal{{Title: "Slipped Deal", Value: 1, Stage: models.DealStageQualified, Status: models.DealStatusOpen, StageEnteredAt: &lastWeek, PreviousStage: &prev}}
	for i := 0; i < 15; i++ {
		deals = append(deals, &models.Deal{Title: "Old Deal", Value: 1, Stage: models.DealStageQualified, Status: models.DealStatusOpen, StageEnteredAt: &longAgo})
	}
	for _, d := range deals {
		d.CompanyID, d.ContactID = company.ID, contact.ID
		require.NoError(t, db.Create(d).Error)
		require.NoError(t, db.Model(d).UpdateColumn("created_at", longAgo).Error)
	}

	w, err := digest.BuildWeekly(db, &config.Config{}, digestNow)
	require.NoError(t, err)
	assert.Contains(t, w.Body, "Slipped Deal (Acme Corp): Negotiation -> Qualified")
}

// TestWeeklyDigest_FailedSendReleasesWeek: the week is claimed before
// sending, and released when every send fails, so the next check retries.
func TestWeeklyDigest_FailedSendReleasesWeek(t *testing.T) {
	_, db := testutil.App(t)
	keepSeedConfig(t, db)
	testutil.CreateUser(t, db, models.RoleAdmin)
	orig := digest.SendMail
	digest.SendMail = func(*config.Config, string, string, string) error { return assert.AnError }
	t.Cleanup(func() { digest.SendMail = orig })
	cfg := &config.Config{SMTPHost: "smtp.example.com"}
	due := time.Date(2026, 9, 21, 9, 0, 0, 0, time.Local)

	require.Error(t, digest.MaybeSendWeekly(db, cfg, due))
	var settings models.AppSettings
	require.NoError(t, db.First(&settings).Error)
	assert.True(t, settings.LastWeeklyDigestAt == nil || settings.LastWeeklyDigestAt.Before(due.Add(-9*time.Hour)), "claim released")

	sent := captureDigestMail(t)
	require.NoError(t, digest.MaybeSendWeekly(db, cfg, due.Add(time.Hour)))
	assert.Len(t, *sent, 1, "retried and sent on the next check")
}

// TestWeeklyDigest_AdminEndpoints: preview is Admin-only and never sends;
// the test send refuses without SMTP; the settings toggle is optional.
func TestWeeklyDigest_AdminEndpoints(t *testing.T) {
	app, db := testutil.App(t)
	keepSeedConfig(t, db)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	sent := captureDigestMail(t)

	var out struct {
		Data struct {
			Subject    string   `json:"subject"`
			Body       string   `json:"body"`
			Recipients []string `json:"recipients"`
			Enabled    bool     `json:"enabled"`
		} `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/weekly-digest/preview", nil, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, &out).StatusCode)
	assert.True(t, strings.HasPrefix(out.Data.Subject, "Weekly pipeline review: "))
	assert.Contains(t, out.Data.Body, "SUMMARY")
	assert.True(t, out.Data.Enabled)
	assert.Empty(t, *sent, "preview never sends")

	req = testutil.AuthRequest(t, http.MethodGet, "/api/v1/admin/weekly-digest/preview", nil, manager.ID, manager.Role)
	assert.Equal(t, fiber.StatusForbidden, doJSON(t, app, req, nil).StatusCode)

	req = testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/weekly-digest/test", nil, admin.ID, admin.Role)
	assert.Equal(t, fiber.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "test environment has no SMTP")

	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 3000000, "annual_revenue_goal": 12000000, "weekly_digest_enabled": false,
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	var s models.AppSettings
	require.NoError(t, db.First(&s).Error)
	assert.False(t, s.WeeklyDigestEnabled)

	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/settings", map[string]interface{}{
		"quarterly_sales_target": 3000000, "annual_revenue_goal": 12000000,
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	require.NoError(t, db.First(&s).Error)
	assert.False(t, s.WeeklyDigestEnabled, "omitting it leaves it alone")
}

// TestStageRename_CarriesRecords: renaming a Deal or Prospect stage repoints
// the records using it (current and previous stage), and a new Deal with no
// stage starts in the renamed first stage.
func TestStageRename_CarriesRecords(t *testing.T) {
	app, db := testutil.App(t)
	keepSeedConfig(t, db)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil) // stage "Lead"
	moved := seedDeal(t, db, nil)
	prevLead := "Lead"
	require.NoError(t, db.Model(moved).UpdateColumns(map[string]interface{}{"stage": "Qualified", "previous_stage": prevLead}).Error)

	var lead models.PipelineStage
	require.NoError(t, db.Where("name = ?", "Lead").First(&lead).Error)
	req := testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/pipeline-stages/"+itoa(lead.ID), map[string]interface{}{
		"name": "Discovery", "sort_order": lead.SortOrder, "is_won_stage": false, "is_lost_stage": false,
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)

	var d1, d2 models.Deal
	require.NoError(t, db.First(&d1, deal.ID).Error)
	require.NoError(t, db.First(&d2, moved.ID).Error)
	assert.Equal(t, models.DealStage("Discovery"), d1.Stage)
	require.NotNil(t, d2.PreviousStage)
	assert.Equal(t, "Discovery", *d2.PreviousStage)

	company := seedCompany(t, db)
	contact := seedContact(t, db, company.ID)
	var created struct {
		Data struct {
			Stage string `json:"stage"`
		} `json:"data"`
	}
	req = testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals", map[string]interface{}{
		"title": "No Stage Given", "value": 10, "company_id": company.ID, "contact_id": contact.ID,
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusCreated, doJSON(t, app, req, &created).StatusCode)
	assert.Equal(t, "Discovery", created.Data.Stage, "defaults to the first open stage, not the literal \"Lead\"")

	prospect := &models.Prospect{Name: "P", Source: "Social Media", Status: models.ProspectStatusNurturing}
	require.NoError(t, db.Create(prospect).Error)
	var nurturing models.ProspectStage
	require.NoError(t, db.Where("name = ?", "Nurturing").First(&nurturing).Error)
	req = testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/prospect-stages/"+itoa(nurturing.ID), map[string]interface{}{
		"name": "Warming Up", "sort_order": nurturing.SortOrder, "is_disqualified_stage": false,
	}, admin.ID, admin.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, req, nil).StatusCode)
	var p models.Prospect
	require.NoError(t, db.First(&p, prospect.ID).Error)
	assert.Equal(t, models.ProspectStatus("Warming Up"), p.Status)
}

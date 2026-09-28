package apitests

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// dropQuoteTemplates deletes the named templates when the test ends —
// quote_templates isn't in testutil's truncate list, so a leftover row shows
// up in other tests' template listings.
func dropQuoteTemplates(t *testing.T, db *gorm.DB, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		assert.NoError(t, db.Where("name IN ?", names).Delete(&models.QuoteTemplate{}).Error)
	})
}

// Every Create whose model has a `default:true` bool stores an explicit
// false as false — a plain db.Create let the column default win, so e.g. a
// product created inactive came back active.
func TestCreate_ExplicitFalseOnDefaultTrueFlags(t *testing.T) {
	app, db := testutil.App(t)
	keepSeedConfig(t, db)
	dropQuoteTemplates(t, db, "No VAT")
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	cases := []struct {
		name  string
		path  string
		body  map[string]interface{}
		model interface{}
		col   string
	}{
		{"product", "/api/v1/products", map[string]interface{}{"name": "Off product", "is_active": false}, &models.Product{}, "is_active"},
		{"quote template", "/api/v1/quote-templates", map[string]interface{}{"name": "No VAT", "vat_enabled": false}, &models.QuoteTemplate{}, "vat_enabled"},
		{"option", "/api/v1/admin/industries", map[string]interface{}{"name": "Off industry", "is_active": false}, &models.IndustryOption{}, "is_active"},
		{"pipeline stage", "/api/v1/admin/pipeline-stages", map[string]interface{}{"name": "Off stage", "sort_order": 50, "is_active": false}, &models.PipelineStage{}, "is_active"},
		{"prospect stage", "/api/v1/admin/prospect-stages", map[string]interface{}{"name": "Off pstage", "sort_order": 50, "is_active": false}, &models.ProspectStage{}, "is_active"},
		{"scoring criterion", "/api/v1/admin/lead-scoring-criteria", map[string]interface{}{"name": "Off rule", "field": "source", "match_value": "Website", "weight": 5, "is_active": false}, &models.LeadScoringCriterion{}, "is_active"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "product" {
				// category must be an active product category.
				require.NoError(t, db.Create(&models.ProductCategoryOption{Name: "Software", IsActive: true}).Error)
				tc.body["category"] = "Software"
			}
			var out struct {
				Data map[string]interface{} `json:"data"`
			}
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, tc.path, tc.body, admin.ID, admin.Role), &out)
			require.Equal(t, http.StatusCreated, resp.StatusCode)
			assert.Equal(t, false, out.Data[tc.col], "response shows the stored value")

			var stored bool
			require.NoError(t, db.Model(tc.model).Where("name = ?", tc.body["name"]).Select(tc.col).Scan(&stored).Error)
			assert.False(t, stored)
		})
	}
}

// vat_enabled became a pointer on the template form: omitted still means
// true, as on Quote Create.
func TestQuoteTemplateCreate_OmittedVatEnabledDefaultsTrue(t *testing.T) {
	app, db := testutil.App(t)
	dropQuoteTemplates(t, db, "Default VAT")
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/quote-templates",
		map[string]interface{}{"name": "Default VAT"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var stored models.QuoteTemplate
	require.NoError(t, db.Where("name = ?", "Default VAT").First(&stored).Error)
	assert.True(t, stored.VatEnabled)
}

func seedClosedDeal(t *testing.T, db *gorm.DB, stage models.DealStage, status models.DealStatus) *models.Deal {
	t.Helper()
	deal := seedDeal(t, db, nil)
	reason := models.LostReasonPrice
	updates := map[string]interface{}{"stage": stage, "status": status}
	if status == models.DealStatusLost {
		updates["lost_reason"] = reason
	}
	require.NoError(t, db.Model(deal).Updates(updates).Error)
	require.NoError(t, db.First(deal, deal.ID).Error)
	return deal
}

// Dragging a Won/Lost deal back to an open stage reopens it; it used to
// keep status won/lost in e.g. Negotiation.
func TestDealUpdateStage_MoveToOpenStageReopens(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	for _, tc := range []struct {
		stage  models.DealStage
		status models.DealStatus
	}{{models.DealStageWon, models.DealStatusWon}, {models.DealStageLost, models.DealStatusLost}} {
		t.Run(string(tc.stage), func(t *testing.T) {
			deal := seedClosedDeal(t, db, tc.stage, tc.status)
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(deal.ID)+"/stage",
				map[string]interface{}{"stage": "Negotiation"}, admin.ID, admin.Role), nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var reloaded models.Deal
			require.NoError(t, db.First(&reloaded, deal.ID).Error)
			assert.Equal(t, models.DealStatusOpen, reloaded.Status)
			assert.Nil(t, reloaded.LostReason)
		})
	}
}

// The edit form resubmits the current status on every save: moving a Won
// deal's stage back to an open one with status "won" still reopens it, and
// a Lost one doesn't demand a lost_reason for a move out of Lost.
func TestDealUpdate_MoveOutOfTerminalStageReopens(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	for _, tc := range []struct {
		stage  models.DealStage
		status models.DealStatus
	}{{models.DealStageWon, models.DealStatusWon}, {models.DealStageLost, models.DealStatusLost}} {
		t.Run(string(tc.stage), func(t *testing.T) {
			deal := seedClosedDeal(t, db, tc.stage, tc.status)
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), map[string]interface{}{
				"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": deal.Title, "value": deal.Value,
				"stage": "Negotiation", "status": tc.status,
			}, admin.ID, admin.Role), nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var reloaded models.Deal
			require.NoError(t, db.First(&reloaded, deal.ID).Error)
			assert.Equal(t, models.DealStatusOpen, reloaded.Status)
			assert.Nil(t, reloaded.LostReason)
		})
	}
}

// A lost-at-this-stage deal (status lost, open stage, unchanged) keeps
// status lost on an unrelated edit — the reopen is only on leaving Won/Lost.
func TestDealUpdate_LostAtOpenStageStaysLost(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedClosedDeal(t, db, models.DealStageNegotiation, models.DealStatusLost)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Renamed", "value": deal.Value,
		"stage": "Negotiation", "status": "lost", "lost_reason": "price",
	}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, models.DealStatusLost, reloaded.Status)
}

// A Deal PUT without stage/status keeps the stored ones; it used to save "".
func TestDealUpdate_OmittedStageAndStatusKeepStored(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(deal).Update("stage", models.DealStageNegotiation).Error)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/deals/"+itoa(deal.ID), map[string]interface{}{
		"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Renamed", "value": 5,
	}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var reloaded models.Deal
	require.NoError(t, db.First(&reloaded, deal.ID).Error)
	assert.Equal(t, models.DealStageNegotiation, reloaded.Stage)
	assert.Equal(t, models.DealStatusOpen, reloaded.Status)
	assert.Equal(t, "Renamed", reloaded.Title)
}

// Lead status must be one of the LeadStatus values on every write path; an
// over-long one used to be a 500 from the varchar(16) column.
func TestLeadStatus_Validated(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	lead := seedLead(t, db, nil)

	for _, status := range []string{"Bogus", strings.Repeat("x", 40)} {
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads",
			map[string]interface{}{"name": "Bad", "status": status}, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "create %q", status)

		resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID),
			map[string]interface{}{"name": lead.Name, "status": status}, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "update %q", status)

		resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/"+itoa(lead.ID)+"/status",
			map[string]interface{}{"status": status}, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "patch %q", status)
	}

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/leads/"+itoa(lead.ID)+"/status",
		map[string]interface{}{"status": "Contacted"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// A Lead PUT without status keeps the stored one; it used to save "".
func TestLeadUpdate_OmittedStatusKeepsStored(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	lead := seedLead(t, db, nil)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/leads/"+itoa(lead.ID),
		map[string]interface{}{"name": "Renamed"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reloaded models.Lead
	require.NoError(t, db.First(&reloaded, lead.ID).Error)
	assert.Equal(t, models.LeadStatusQualified, reloaded.Status)
}

// Same for a Prospect PUT.
func TestProspectUpdate_OmittedStatusKeepsStored(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	prospect := seedProspect(t, db, nil)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/prospects/"+itoa(prospect.ID),
		map[string]interface{}{"name": "Renamed", "source": prospect.Source}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reloaded models.Prospect
	require.NoError(t, db.First(&reloaded, prospect.ID).Error)
	assert.Equal(t, models.ProspectStatusEngaging, reloaded.Status)
}

// Lead→Deal Convert runs Deal Create's checks on the new deal.
func TestLeadConvert_DealValidation(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	other := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)
	otherCompany := seedCompany(t, db)
	foreignContact := seedContact(t, db, otherCompany.ID)

	convert := func(userID uint, role models.Role, body map[string]interface{}) (*http.Response, *models.Lead) {
		t.Helper()
		lead := &models.Lead{Name: "Convert me", CompanyID: &company.ID, Source: models.LeadSourceWebsite,
			Status: models.LeadStatusQualified, AssignedTo: &rep.ID}
		require.NoError(t, db.Create(lead).Error)
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", body, userID, role), nil)
		return resp, lead
	}
	dealFor := func(lead *models.Lead) *models.Deal {
		t.Helper()
		var deal models.Deal
		if err := db.Where("lead_id = ?", lead.ID).First(&deal).Error; err != nil {
			return nil
		}
		return &deal
	}

	t.Run("won stage sets status won", func(t *testing.T) {
		setRequireSignedContract(t, db, false)
		resp, lead := convert(admin.ID, admin.Role, map[string]interface{}{"deal": map[string]interface{}{"stage": "Won"}})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, models.DealStatusWon, dealFor(lead).Status)
	})
	t.Run("won stage blocked by signed-contract gate", func(t *testing.T) {
		setRequireSignedContract(t, db, true)
		resp, lead := convert(admin.ID, admin.Role, map[string]interface{}{"deal": map[string]interface{}{"stage": "Won"}})
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		assert.Nil(t, dealFor(lead))
	})
	t.Run("lost stage needs lost_reason", func(t *testing.T) {
		resp, lead := convert(admin.ID, admin.Role, map[string]interface{}{"deal": map[string]interface{}{"stage": "Lost"}})
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		assert.Nil(t, dealFor(lead))

		resp, lead = convert(admin.ID, admin.Role, map[string]interface{}{"deal": map[string]interface{}{"stage": "Lost", "lost_reason": "price"}})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		deal := dealFor(lead)
		assert.Equal(t, models.DealStatusLost, deal.Status)
		require.NotNil(t, deal.LostReason)
		assert.Equal(t, models.LostReasonPrice, *deal.LostReason)
	})
	t.Run("negative value and bad date", func(t *testing.T) {
		resp, _ := convert(admin.ID, admin.Role, map[string]interface{}{"deal": map[string]interface{}{"value": -1}})
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		resp, _ = convert(admin.ID, admin.Role, map[string]interface{}{"deal": map[string]interface{}{"expected_close_date": "soon"}})
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	})
	t.Run("sales rep cannot assign to another rep", func(t *testing.T) {
		resp, lead := convert(rep.ID, rep.Role, map[string]interface{}{"deal": map[string]interface{}{"assigned_to": other.ID}})
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Nil(t, dealFor(lead))
	})
	t.Run("missing company or contact is 404", func(t *testing.T) {
		resp, _ := convert(admin.ID, admin.Role, map[string]interface{}{"company_id": 999999})
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp, _ = convert(admin.ID, admin.Role, map[string]interface{}{"contact_id": 999999})
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
	t.Run("contact from another company is 422", func(t *testing.T) {
		resp, lead := convert(admin.ID, admin.Role, map[string]interface{}{"company_id": company.ID, "contact_id": foreignContact.ID})
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		assert.Nil(t, dealFor(lead))
	})
}

// Prospect→Lead Convert: an explicit company/contact that doesn't exist is
// 404, not 500.
func TestProspectConvert_MissingCompanyIs404(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	prospect := seedProspect(t, db, nil)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert",
		map[string]interface{}{"company_id": 999999}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// convertConcurrently fires two Converts of the same row while the test
// holds that row's lock, so both pass the handler's unlocked pre-check
// before either can convert — then releases it. The in-transaction locked
// re-check must turn the second into a 409.
func convertConcurrently(t *testing.T, db *gorm.DB, send func() int, row interface{}, id uint) []int {
	t.Helper()
	blocker := db.Begin()
	require.NoError(t, blocker.Clauses(clause.Locking{Strength: "UPDATE"}).First(row, id).Error)

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = send()
		}(i)
	}

	// Wait until both requests are queued on the row lock (a row-lock wait
	// is "transactionid"/"tuple"; other test binaries queued on testutil's
	// advisory lock don't count).
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int64
		require.NoError(t, db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event IN ('transactionid', 'tuple')").Scan(&waiting).Error)
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			blocker.Rollback()
			t.Fatalf("only %d converts reached the row lock", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, blocker.Rollback().Error)
	wg.Wait()
	return codes
}

func TestLeadConvert_ConcurrentSecondIsConflict(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	lead := seedLead(t, db, nil)

	codes := convertConcurrently(t, db, func() int {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{}, admin.ID, admin.Role)
		resp, err := app.Test(req, -1)
		if err != nil {
			return 0
		}
		return resp.StatusCode
	}, &models.Lead{}, lead.ID)
	assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, codes)

	var deals int64
	require.NoError(t, db.Model(&models.Deal{}).Where("lead_id = ?", lead.ID).Count(&deals).Error)
	assert.Equal(t, int64(1), deals, "exactly one Deal per converted Lead")
}

func TestProspectConvert_ConcurrentSecondIsConflict(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	prospect := seedProspect(t, db, nil)

	codes := convertConcurrently(t, db, func() int {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/prospects/"+itoa(prospect.ID)+"/convert", map[string]interface{}{}, admin.ID, admin.Role)
		resp, err := app.Test(req, -1)
		if err != nil {
			return 0
		}
		return resp.StatusCode
	}, &models.Prospect{}, prospect.ID)
	assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, codes)

	var leads int64
	require.NoError(t, db.Model(&models.Lead{}).Where("prospect_id = ?", prospect.ID).Count(&leads).Error)
	assert.Equal(t, int64(1), leads, "exactly one Lead per converted Prospect")
}

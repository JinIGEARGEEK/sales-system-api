package apitests

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// errBody decodes the §1.5 error envelope.
type errBody struct {
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
	} `json:"error"`
}

// seedWonDeal is a Won Deal assigned to owner, with no money attached yet.
func seedWonDeal(t *testing.T, db *gorm.DB, owner *uint) *models.Deal {
	t.Helper()
	deal := seedDeal(t, db, owner)
	require.NoError(t, db.Model(deal).Updates(map[string]interface{}{
		"stage": models.DealStageWon, "status": models.DealStatusWon,
	}).Error)
	require.NoError(t, db.First(deal, deal.ID).Error)
	return deal
}

// seedWonDealWithPayment is a Won Deal with one recorded Payment.
func seedWonDealWithPayment(t *testing.T, db *gorm.DB, owner *uint) *models.Deal {
	t.Helper()
	deal := seedWonDeal(t, db, owner)
	require.NoError(t, db.Create(&models.Payment{DealID: deal.ID, Amount: 500, PaidAt: time.Now()}).Error)
	return deal
}

// lastAudit returns the newest audit entry for a Deal with that action.
func lastAudit(t *testing.T, db *gorm.DB, entityType string, id uint, action string) *models.AuditLogEntry {
	t.Helper()
	var entries []models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", entityType, id, action).
		Order("id DESC").Limit(1).Find(&entries).Error)
	if len(entries) == 0 {
		return nil
	}
	return &entries[0]
}

func withReason(path, reason string) string {
	return path + "?reason=" + url.QueryEscape(reason)
}

// What counts as money: a live Payment, any installment, a signed Contract.
// A deleted Payment or an unsigned Contract doesn't.
func TestWonDealDelete_MoneyKinds(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	cases := []struct {
		name      string
		attach    func(dealID uint)
		protected bool
	}{
		{"payment", func(id uint) {
			require.NoError(t, db.Create(&models.Payment{DealID: id, Amount: 100, PaidAt: time.Now()}).Error)
		}, true},
		{"installment", func(id uint) {
			require.NoError(t, db.Create(&models.PaymentInstallment{DealID: id, Amount: 100, DueDate: time.Now()}).Error)
		}, true},
		{"signed contract", func(id uint) {
			require.NoError(t, db.Create(&models.Contract{DealID: id, Status: models.ContractStatusSigned}).Error)
		}, true},
		{"deleted payment", func(id uint) {
			p := &models.Payment{DealID: id, Amount: 100, PaidAt: time.Now()}
			require.NoError(t, db.Create(p).Error)
			require.NoError(t, db.Delete(p).Error)
		}, false},
		{"draft contract", func(id uint) {
			require.NoError(t, db.Create(&models.Contract{DealID: id, Status: models.ContractStatusDraft}).Error)
		}, false},
		{"nothing", func(uint) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deal := seedWonDeal(t, db, &rep.ID)
			tc.attach(deal.ID)

			var body errBody
			var out interface{} // a 204 has no body to decode
			if tc.protected {
				out = &body
			}
			resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/deals/"+itoa(deal.ID), nil, rep.ID, rep.Role), out)
			if tc.protected {
				require.Equal(t, http.StatusConflict, resp.StatusCode)
				assert.Equal(t, "WON_DEAL_PROTECTED", body.Error.Code)
				var n int64
				db.Model(&models.Deal{}).Where("id = ?", deal.ID).Count(&n)
				assert.EqualValues(t, 1, n, "still there")
			} else {
				require.Equal(t, http.StatusNoContent, resp.StatusCode)
			}
		})
	}
}

// A manager may delete a protected Won Deal only with ?reason=; the reason
// lands in the deleted audit entry, and Restore writes its own entry.
func TestWonDealDelete_ManagerNeedsReason_AuditsDeleteAndRestore(t *testing.T) {
	app, db := testutil.App(t)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	deal := seedWonDealWithPayment(t, db, nil)
	path := "/api/v1/deals/" + itoa(deal.ID)

	var body errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, path, nil, manager.ID, manager.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "REASON_REQUIRED", body.Error.Code)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, withReason(path, "   "), nil, manager.ID, manager.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode, "a blank reason is no reason")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, withReason(path, "duplicate of #12"), nil, manager.ID, manager.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	entry := lastAudit(t, db, "deal", deal.ID, "deleted")
	require.NotNil(t, entry)
	assert.Equal(t, "duplicate of #12", entry.After["reason"])
	assert.Equal(t, "won", entry.Before["status"])
	assert.EqualValues(t, manager.ID, entry.ActorID)

	var restored struct {
		Data models.Deal `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, path+"/restore", nil, manager.ID, manager.Role), &restored)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.False(t, restored.Data.DeletedAt.Valid, "the response shows the restored row")
	assert.Nil(t, restored.Data.DeletedBy)
	require.NotNil(t, lastAudit(t, db, "deal", deal.ID, "restored"))
}

// An ordinary delete is audited too, without a reason.
func TestDealDelete_AuditedWithoutReason(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, &rep.ID)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/deals/"+itoa(deal.ID), nil, rep.ID, rep.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	entry := lastAudit(t, db, "deal", deal.ID, "deleted")
	require.NotNil(t, entry)
	_, hasReason := entry.After["reason"]
	assert.False(t, hasReason)
}

// Kanban move out of Won: 409 for a rep, 409 for a manager without a reason,
// and with one the move goes through with a won_reversed audit entry.
func TestWonDealUnwin_KanbanMove(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	manager := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedWonDealWithPayment(t, db, &rep.ID)
	path := "/api/v1/deals/" + itoa(deal.ID) + "/stage"

	toOpen := map[string]interface{}{"stage": models.DealStageNegotiation}
	var body errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path, toOpen, rep.ID, rep.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "WON_DEAL_PROTECTED", body.Error.Code)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, withReason(path, "reason"), toOpen, rep.ID, rep.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode, "a reason doesn't help a non-manager")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path, toOpen, manager.ID, manager.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "REASON_REQUIRED", body.Error.Code)

	var out struct {
		Data models.Deal `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, withReason(path, "customer cancelled, refund pending"), toOpen, manager.ID, manager.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, models.DealStatusOpen, out.Data.Status)

	entry := lastAudit(t, db, "deal", deal.ID, "won_reversed")
	require.NotNil(t, entry)
	assert.Equal(t, "customer cancelled, refund pending", entry.After["reason"])
	assert.Equal(t, "won", entry.Before["status"])
	assert.Equal(t, "open", entry.After["status"])
	require.NotNil(t, lastAudit(t, db, "deal", deal.ID, "stage_changed"), "the move's own entry is still written")

	// Repositioning within the Won lane isn't an un-win.
	other := seedWonDealWithPayment(t, db, &rep.ID)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/"+itoa(other.ID)+"/stage",
		map[string]interface{}{"stage": models.DealStageWon, "position": 3.5}, rep.ID, rep.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// Moving a protected Won Deal to Lost needs the manager's reason and a
// lost_reason.
func TestWonDealUnwin_KanbanToLost(t *testing.T) {
	app, db := testutil.App(t)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	deal := seedWonDealWithPayment(t, db, nil)
	path := withReason("/api/v1/deals/"+itoa(deal.ID)+"/stage", "chargeback")

	var body errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path,
		map[string]interface{}{"stage": models.DealStageLost}, manager.ID, manager.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, body.Error.Fields, "lost_reason")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path,
		map[string]interface{}{"stage": models.DealStageLost, "lost_reason": models.LostReasonOther}, manager.ID, manager.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var stored models.Deal
	require.NoError(t, db.First(&stored, deal.ID).Error)
	assert.Equal(t, models.DealStatusLost, stored.Status)
	require.NotNil(t, lastAudit(t, db, "deal", deal.ID, "won_reversed"))
}

// PUT that moves a protected Won Deal out of Won follows the same rule; a
// PUT that keeps it Won is unaffected.
func TestWonDealUnwin_Put(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	deal := seedWonDealWithPayment(t, db, &rep.ID)
	path := "/api/v1/deals/" + itoa(deal.ID)
	form := func(stage models.DealStage) map[string]interface{} {
		return map[string]interface{}{
			"company_id": deal.CompanyID, "contact_id": deal.ContactID, "title": "Renamed",
			"value": deal.Value, "stage": stage, "assigned_to": rep.ID,
		}
	}

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, path, form(models.DealStageWon), rep.ID, rep.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "editing a Won Deal that stays Won is fine")

	var body errBody
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, path, form(models.DealStageNegotiation), rep.ID, rep.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "WON_DEAL_PROTECTED", body.Error.Code)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, path, form(models.DealStageNegotiation), manager.ID, manager.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "REASON_REQUIRED", body.Error.Code)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, withReason(path, "re-opened for scope change"), form(models.DealStageNegotiation), manager.ID, manager.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	entry := lastAudit(t, db, "deal", deal.ID, "won_reversed")
	require.NotNil(t, entry)
	assert.Equal(t, "re-opened for scope change", entry.After["reason"])
}

// Bulk archive skips a protected Won Deal and reports it, archiving the rest.
func TestWonDealBulkArchive_SkipsProtected(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	protected := seedWonDealWithPayment(t, db, nil)
	plain := seedDeal(t, db, nil)

	var out struct {
		Data struct {
			Archived []uint `json:"archived"`
			Skipped  []struct {
				ID     uint   `json:"id"`
				Reason string `json:"reason"`
			} `json:"skipped"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/deals/bulk-archive",
		map[string]interface{}{"ids": []uint{protected.ID, plain.ID}}, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []uint{plain.ID}, out.Data.Archived)
	require.Len(t, out.Data.Skipped, 1)
	assert.Equal(t, protected.ID, out.Data.Skipped[0].ID)
	assert.Equal(t, "won_deal_with_money", out.Data.Skipped[0].Reason)

	var n int64
	db.Model(&models.Deal{}).Where("id = ?", protected.ID).Count(&n)
	assert.EqualValues(t, 1, n, "protected deal still live")
	db.Model(&models.Deal{}).Where("id = ?", plain.ID).Count(&n)
	assert.Zero(t, n, "plain deal archived")
	assert.Nil(t, lastAudit(t, db, "deal", protected.ID, "bulk_archived"), "no audit for a skipped row")
	assert.NotNil(t, lastAudit(t, db, "deal", plain.ID, "bulk_archived"))
}

// Kanban move into Lost requires lost_reason, like PUT; a Deal already Lost
// with one stored can be repositioned without it.
func TestDealUpdateStage_LostRequiresReason(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	path := "/api/v1/deals/" + itoa(deal.ID) + "/stage"

	var body errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path,
		map[string]interface{}{"stage": models.DealStageLost}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, "VALIDATION_ERROR", body.Error.Code)
	assert.Equal(t, []string{"required"}, body.Error.Fields["lost_reason"])

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path,
		map[string]interface{}{"stage": models.DealStageLost, "lost_reason": ""}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "empty counts as missing")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path,
		map[string]interface{}{"stage": models.DealStageLost, "lost_reason": models.LostReasonPrice}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, path,
		map[string]interface{}{"stage": models.DealStageLost, "position": 2.5}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "repositioning within Lost keeps the stored reason")
	var stored models.Deal
	require.NoError(t, db.First(&stored, deal.ID).Error)
	require.NotNil(t, stored.LostReason)
	assert.Equal(t, models.LostReasonPrice, *stored.LostReason)
}

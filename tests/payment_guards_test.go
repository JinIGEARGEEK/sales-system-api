package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

type paymentResp struct {
	Data models.Payment `json:"data"`
}

func paymentsPath(dealID uint) string { return "/api/v1/deals/" + itoa(dealID) + "/payments" }

// Create/Update/Delete each write a payment audit entry with before/after.
func TestPayment_AuditTrail(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	var created paymentResp
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 400, "method": "transfer"}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := created.Data.ID
	entry := lastAudit(t, db, "payment", id, "created")
	require.NotNil(t, entry)
	assert.Nil(t, entry.Before)
	assert.EqualValues(t, 400, entry.After["amount"])
	require.NotNil(t, created.Data.CreatedBy)
	assert.Equal(t, admin.ID, *created.Data.CreatedBy)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(id),
		map[string]interface{}{"amount": 450, "note": "corrected"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	entry = lastAudit(t, db, "payment", id, "updated")
	require.NotNil(t, entry)
	assert.EqualValues(t, 400, entry.Before["amount"])
	assert.EqualValues(t, 450, entry.After["amount"])
	assert.Equal(t, "corrected", entry.After["note"])

	// A no-op save writes nothing.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(id),
		map[string]interface{}{"note": "corrected"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var updates int64
	db.Model(&models.AuditLogEntry{}).Where("entity_type = ? AND entity_id = ? AND action = ?", "payment", id, "updated").Count(&updates)
	assert.EqualValues(t, 1, updates)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/payments/"+itoa(id), nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	entry = lastAudit(t, db, "payment", id, "deleted")
	require.NotNil(t, entry)
	assert.EqualValues(t, 450, entry.Before["amount"])
	assert.Nil(t, entry.After)
}

// Delete is soft: the row stays (deleted_at/deleted_by set) but drops out of
// the list, its totals and the installment waterfall.
func TestPayment_SoftDelete(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	inst := &models.PaymentInstallment{DealID: deal.ID, Amount: 300, DueDate: time.Now().AddDate(0, 0, 5)}
	require.NoError(t, db.Create(inst).Error)
	keep := &models.Payment{DealID: deal.ID, Amount: 100, PaidAt: time.Now()}
	gone := &models.Payment{DealID: deal.ID, Amount: 300, PaidAt: time.Now()}
	require.NoError(t, db.Create(keep).Error)
	require.NoError(t, db.Create(gone).Error)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/payments/"+itoa(gone.ID), nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var stored models.Payment
	require.NoError(t, db.Unscoped().First(&stored, gone.ID).Error, "row kept")
	assert.True(t, stored.DeletedAt.Valid)
	require.NotNil(t, stored.DeletedBy)
	assert.Equal(t, admin.ID, *stored.DeletedBy)

	var list struct {
		Data struct {
			Payments  []models.Payment `json:"payments"`
			TotalPaid float64          `json:"total_paid"`
		} `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, paymentsPath(deal.ID), nil, admin.ID, admin.Role), &list)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, list.Data.Payments, 1)
	assert.Equal(t, keep.ID, list.Data.Payments[0].ID)
	assert.InDelta(t, 100, list.Data.TotalPaid, 0.001)

	var schedule struct {
		Data []struct {
			Covered float64 `json:"covered"`
		} `json:"data"`
	}
	doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", nil, admin.ID, admin.Role), &schedule)
	require.Len(t, schedule.Data, 1)
	assert.InDelta(t, 100, schedule.Data[0].Covered, 0.001, "the deleted payment no longer covers the installment")

	// A deleted payment no longer protects a Won Deal.
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", deal.ID).
		Updates(map[string]interface{}{"stage": models.DealStageWon, "status": models.DealStatusWon}).Error)
	require.NoError(t, db.Delete(&models.PaymentInstallment{}, inst.ID).Error)
	require.NoError(t, db.Delete(keep).Error)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/deals/"+itoa(deal.ID), nil, rep.ID, rep.Role), nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPayment_RejectsLostDeal(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedClosedDeal(t, db, models.DealStageLost, models.DealStatusLost)

	var body errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 100}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, body.Error.Fields, "deal_id")
}

// paid_at may be any time today (server-local) but not tomorrow.
func TestPayment_RejectsFuturePaidAt(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	now := time.Now().In(time.Local)
	endOfToday := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, time.Local)
	tomorrow := endOfToday.Add(2 * time.Minute)

	var body errBody
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 100, "paid_at": tomorrow.Format(time.RFC3339)}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, body.Error.Fields, "paid_at")

	var created paymentResp
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 100, "paid_at": endOfToday.Format(time.RFC3339)}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "later today is fine")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(created.Data.ID),
		map[string]interface{}{"paid_at": tomorrow.Format(time.RFC3339)}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "Update checks it too")
}

// A non-empty document_number is unique among non-deleted payments, across
// Deals; a deleted payment's number can be reused.
func TestPayment_DuplicateDocumentNumber(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	dealA := seedDeal(t, db, nil)
	dealB := seedDeal(t, db, nil)

	var first paymentResp
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(dealA.ID),
		map[string]interface{}{"amount": 100, "document_number": "RE-001"}, admin.ID, admin.Role), &first)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var body errBody
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(dealB.ID),
		map[string]interface{}{"amount": 100, "document_number": " RE-001 "}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "CONFLICT", body.Error.Code)

	// Blank numbers never collide.
	for i := 0; i < 2; i++ {
		resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(dealB.ID),
			map[string]interface{}{"amount": 10, "document_number": ""}, admin.ID, admin.Role), nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	// Update into a taken number is a 409; resaving its own number isn't.
	var second paymentResp
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(dealB.ID),
		map[string]interface{}{"amount": 10, "document_number": "RE-002"}, admin.ID, admin.Role), &second)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(second.Data.ID),
		map[string]interface{}{"document_number": "RE-001"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(first.Data.ID),
		map[string]interface{}{"document_number": "RE-001", "note": "x"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Once the first is deleted its number is free.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/payments/"+itoa(first.Data.ID), nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(dealB.ID),
		map[string]interface{}{"amount": 10, "document_number": "RE-001"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

// Cash + WHT beyond the receivable is a 422 unless allow_overpayment; the
// receivable is the Deal value (1000 here) or the Accepted Quote incl. VAT.
func TestPayment_Overpayment(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil) // value 1000

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 970, "wht_amount": 30}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "exactly the receivable, cash + WHT")

	var body errBody
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 1}, admin.ID, admin.Role), &body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, []string{"exceeds_receivable"}, body.Error.Fields["amount"])

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 0.004}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "within MoneyEpsilon")

	var over paymentResp
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(deal.ID),
		map[string]interface{}{"amount": 50, "allow_overpayment": true}, admin.ID, admin.Role), &over)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	entry := lastAudit(t, db, "payment", over.Data.ID, "created")
	require.NotNil(t, entry)
	assert.Equal(t, true, entry.After["overpayment_allowed"])

	// An already-overpaid Deal still lets you edit a payment without raising it.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(over.Data.ID),
		map[string]interface{}{"wht_certificate_received": true, "amount": 40}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(over.Data.ID),
		map[string]interface{}{"amount": 60}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "raising it still needs the flag")

	// An Accepted Quote with priced items sets the receivable: 2000 + 7% VAT.
	quoted := seedDeal(t, db, nil)
	require.NoError(t, db.Create(&models.Quote{
		DealID: quoted.ID, Status: models.QuoteStatusAccepted, VatEnabled: true,
		Items: models.JSONItems{{Qty: 1, Price: 2000}},
	}).Error)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(quoted.ID),
		map[string]interface{}{"amount": 2140}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, paymentsPath(quoted.ID),
		map[string]interface{}{"amount": 1}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// Installment Update/Delete write payment_installment audit entries.
func TestPaymentInstallment_AuditTrail(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	due := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	inst := &models.PaymentInstallment{DealID: deal.ID, Amount: 500, DueDate: due, Note: "first"}
	require.NoError(t, db.Create(inst).Error)
	linked := &models.Payment{DealID: deal.ID, Amount: 10, PaidAt: time.Now(), InstallmentID: &inst.ID}
	require.NoError(t, db.Create(linked).Error)
	require.NoError(t, db.Delete(linked).Error)

	path := "/api/v1/payment-installments/" + itoa(inst.ID)
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, path,
		map[string]interface{}{"amount": 600, "due_date": due.Format(time.RFC3339), "note": "first"}, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	entry := lastAudit(t, db, "payment_installment", inst.ID, "updated")
	require.NotNil(t, entry)
	assert.EqualValues(t, 500, entry.Before["amount"])
	assert.EqualValues(t, 600, entry.After["amount"])

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, path, nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	entry = lastAudit(t, db, "payment_installment", inst.ID, "deleted")
	require.NotNil(t, entry)
	assert.EqualValues(t, 600, entry.Before["amount"])

	var stored models.Payment
	require.NoError(t, db.Unscoped().First(&stored, linked.ID).Error)
	assert.Nil(t, stored.InstallmentID, "a deleted payment is unlinked too")
}

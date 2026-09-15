package apitests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// TestPaymentInstallmentCreate_RoundTrips guards the basic Create -> List
// round trip, and that List returns a derived status (not a stored one —
// see PaymentInstallment's own doc comment).
func TestPaymentInstallmentCreate_RoundTrips(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	dueDate := time.Now().AddDate(0, 1, 0)

	var out struct {
		Data models.PaymentInstallment `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", map[string]interface{}{
		"amount": 30000, "due_date": dueDate.Format(time.RFC3339),
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, deal.ID, out.Data.DealID)
	assert.InDelta(t, 30000.0, out.Data.Amount, 0.001)

	var listOut struct {
		Data []utils.InstallmentStatus `json:"data"`
	}
	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", nil, admin.ID, admin.Role)
	listResp := doJSON(t, app, listReq, &listOut)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	require.Len(t, listOut.Data, 1)
	assert.Equal(t, utils.InstallmentStatusUpcoming, listOut.Data[0].Status)
}

// TestPaymentInstallmentCreate_RejectsMissingFields guards amount>0 and
// due_date being required, matching Payment's own required-field shape.
func TestPaymentInstallmentCreate_RejectsMissingFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", map[string]interface{}{
		"amount": 0,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestPaymentInstallmentList_StatusReflectsPayments guards the waterfall
// integration end-to-end: a Payment logged against the Deal changes the
// installment's derived status without any explicit link between the two.
func TestPaymentInstallmentList_StatusReflectsPayments(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Create(&models.PaymentInstallment{
		DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 1, 0),
	}).Error)
	require.NoError(t, db.Create(&models.Payment{
		DealID: deal.ID, Amount: 10000, PaidAt: time.Now(), Method: models.PaymentMethodTransfer,
	}).Error)

	var listOut struct {
		Data []utils.InstallmentStatus `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &listOut)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, listOut.Data, 1)
	assert.Equal(t, utils.InstallmentStatusPaid, listOut.Data[0].Status)
}

// TestPaymentInstallmentUpdate_ChangesFields guards Update accepting a full
// field set, same shape as Create.
func TestPaymentInstallmentUpdate_ChangesFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	installment := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 1, 0)}
	require.NoError(t, db.Create(installment).Error)

	newDueDate := time.Now().AddDate(0, 2, 0)
	var out struct {
		Data models.PaymentInstallment `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPut, "/api/v1/payment-installments/"+itoa(installment.ID), map[string]interface{}{
		"amount": 15000, "due_date": newDueDate.Format(time.RFC3339), "note": "revised",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.InDelta(t, 15000.0, out.Data.Amount, 0.001)
	assert.Equal(t, "revised", out.Data.Note)
}

// TestPaymentInstallmentDelete_RemovesRow guards the hard-delete Delete path.
func TestPaymentInstallmentDelete_RemovesRow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	installment := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 1, 0)}
	require.NoError(t, db.Create(installment).Error)

	req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/payment-installments/"+itoa(installment.ID), nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	var count int64
	db.Model(&models.PaymentInstallment{}).Where("id = ?", installment.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

// TestPaymentInstallmentCreate_RejectsWrongOwner guards the same
// dealForSubResource/CanWrite RBAC every other Deal-sub-resource handler
// enforces — a Sales Rep who doesn't own the Deal can't add an installment.
func TestPaymentInstallmentCreate_RejectsWrongOwner(t *testing.T) {
	app, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	other := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, &owner.ID)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", map[string]interface{}{
		"amount": 10000, "due_date": time.Now().AddDate(0, 1, 0).Format(time.RFC3339),
	}, other.ID, other.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestPaymentInstallmentBulkCreate_CreatesAllInOneCall guards the "generate
// schedule" bulk endpoint: N installments created from one request/one
// transaction, all reflected in a subsequent List.
func TestPaymentInstallmentBulkCreate_CreatesAllInOneCall(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	var out struct {
		Data []models.PaymentInstallment `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments/bulk", map[string]interface{}{
		"installments": []map[string]interface{}{
			{"amount": 10000, "due_date": time.Now().AddDate(0, 1, 0).Format(time.RFC3339)},
			{"amount": 10000, "due_date": time.Now().AddDate(0, 2, 0).Format(time.RFC3339)},
			{"amount": 10000, "due_date": time.Now().AddDate(0, 3, 0).Format(time.RFC3339)},
		},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Len(t, out.Data, 3)

	var listOut struct {
		Data []utils.InstallmentStatus `json:"data"`
	}
	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", nil, admin.ID, admin.Role)
	listResp := doJSON(t, app, listReq, &listOut)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Len(t, listOut.Data, 3)
}

// TestPaymentInstallmentBulkCreate_RejectsEmptyList guards the non-empty
// check.
func TestPaymentInstallmentBulkCreate_RejectsEmptyList(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments/bulk", map[string]interface{}{
		"installments": []map[string]interface{}{},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestPaymentInstallmentBulkCreate_RejectsInvalidRow guards that per-row
// validation (amount>0, due_date required) still applies inside the batch —
// one bad row fails the whole request rather than silently skipping it.
func TestPaymentInstallmentBulkCreate_RejectsInvalidRow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments/bulk", map[string]interface{}{
		"installments": []map[string]interface{}{
			{"amount": 10000, "due_date": time.Now().AddDate(0, 1, 0).Format(time.RFC3339)},
			{"amount": 0, "due_date": time.Now().AddDate(0, 2, 0).Format(time.RFC3339)},
		},
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var listOut struct {
		Data []utils.InstallmentStatus `json:"data"`
	}
	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", nil, admin.ID, admin.Role)
	listResp := doJSON(t, app, listReq, &listOut)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Empty(t, listOut.Data, "an invalid row must fail the whole batch, not partially insert")
}

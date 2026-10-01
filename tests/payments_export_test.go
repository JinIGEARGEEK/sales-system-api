package apitests

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// exportPayments GETs /payments/export?query as user and returns the status
// and, on 200, the parsed CSV rows (header first).
func exportPayments(t *testing.T, app *fiber.App, user *models.User, query string) (int, [][]string, http.Header) {
	t.Helper()
	resp, err := app.Test(testutil.AuthRequest(t, http.MethodGet, "/api/v1/payments/export?"+query, nil, user.ID, user.Role), -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, resp.Header
	}
	rows, err := csv.NewReader(resp.Body).ReadAll()
	require.NoError(t, err)
	return resp.StatusCode, rows, resp.Header
}

func localDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 10, 0, 0, 0, time.Local)
}

func TestPaymentsExport_Roles(t *testing.T) {
	app, db := testutil.App(t)
	for role, want := range map[models.Role]int{
		models.RoleAdmin: http.StatusOK, models.RoleSalesManager: http.StatusOK,
		models.RoleSalesRep: http.StatusForbidden, models.RoleMarketing: http.StatusForbidden,
		models.RoleProduction: http.StatusForbidden,
	} {
		u := testutil.CreateUser(t, db, role)
		status, _, _ := exportPayments(t, app, u, "")
		assert.Equal(t, want, status, role)
	}
}

func TestPaymentsExport_RowsAndFilters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	deal := seedDeal(t, db, nil) // company "Acme Corp"
	other := seedDeal(t, db, nil)
	gone := seedDeal(t, db, nil)
	inst := &models.PaymentInstallment{DealID: deal.ID, Amount: 1000, DueDate: localDay(2026, 9, 30)}
	require.NoError(t, db.Create(inst).Error)

	doc := "RE-001"
	p1 := &models.Payment{DealID: deal.ID, Amount: 970, WhtAmount: 30, PaidAt: localDay(2026, 9, 1),
		Method: models.PaymentMethodTransfer, Note: "=first", DocumentNumber: &doc, InstallmentID: &inst.ID}
	p1.CreatedBy = &admin.ID
	p2 := &models.Payment{DealID: deal.ID, Amount: 500, PaidAt: localDay(2026, 9, 15), Method: models.PaymentMethodCash}
	p3 := &models.Payment{DealID: other.ID, Amount: 200, PaidAt: localDay(2026, 9, 20), Method: models.PaymentMethodCash}
	deletedPay := &models.Payment{DealID: deal.ID, Amount: 1, PaidAt: localDay(2026, 9, 10), Method: models.PaymentMethodCash}
	onGone := &models.Payment{DealID: gone.ID, Amount: 2, PaidAt: localDay(2026, 9, 10), Method: models.PaymentMethodCash}
	for _, p := range []*models.Payment{p1, p2, p3, deletedPay, onGone} {
		require.NoError(t, db.Create(p).Error)
	}
	require.NoError(t, db.Delete(deletedPay).Error)
	require.NoError(t, db.Delete(gone).Error)

	status, rows, header := exportPayments(t, app, admin, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, `attachment; filename="payments-`+time.Now().Format("20060102")+`.csv"`, header.Get("Content-Disposition"))
	assert.True(t, strings.HasPrefix(header.Get("Content-Type"), "text/csv"))
	require.Len(t, rows, 4, "header + 3 live payments; deleted payment and payment on deleted deal excluded")
	assert.Equal(t, []string{"Paid At", "Document Number", "Deal ID", "Deal", "Company", "Amount", "WHT Amount", "Total",
		"Method", "Installment ID", "Installment Due Date", "Note", "Created By"}, rows[0])
	assert.Equal(t, []string{"2026-09-01", "RE-001", itoa(deal.ID), "Test Deal", "Acme Corp", "970.00", "30.00", "1000.00",
		"transfer", itoa(inst.ID), "2026-09-30", "'=first", admin.FirstName + " " + admin.LastName}, rows[1])
	assert.Equal(t, "2026-09-15", rows[2][0], "oldest paid_at first")
	assert.Equal(t, "", rows[2][1])
	assert.Equal(t, "", rows[2][9])
	assert.Equal(t, "", rows[2][12])

	ids := func(rows [][]string) []string {
		var out []string
		for _, r := range rows[1:] {
			out = append(out, r[0]+"/"+r[5])
		}
		return out
	}
	_, rows, _ = exportPayments(t, app, admin, "date_from=2026-09-15&date_to=2026-09-20")
	assert.Equal(t, []string{"2026-09-15/500.00", "2026-09-20/200.00"}, ids(rows), "inclusive local days")
	_, rows, _ = exportPayments(t, app, admin, "date_to=2026-09-01")
	assert.Equal(t, []string{"2026-09-01/970.00"}, ids(rows))
	_, rows, _ = exportPayments(t, app, admin, "deal_id="+itoa(other.ID))
	assert.Equal(t, []string{"2026-09-20/200.00"}, ids(rows))
	_, rows, _ = exportPayments(t, app, admin, "company_id="+itoa(deal.CompanyID))
	assert.Equal(t, []string{"2026-09-01/970.00", "2026-09-15/500.00"}, ids(rows))
	_, rows, _ = exportPayments(t, app, admin, "method=cash")
	assert.Equal(t, []string{"2026-09-15/500.00", "2026-09-20/200.00"}, ids(rows))
	_, rows, _ = exportPayments(t, app, admin, "deal_id="+itoa(gone.ID))
	assert.Len(t, rows, 1, "header only")
}

func TestPaymentsExport_BadFilters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	for _, q := range []string{
		"date_from=2026-09-20&date_to=2026-09-01", "date_from=nope", "deal_id=abc", "company_id=-1", "method=bitcoin",
	} {
		status, _, _ := exportPayments(t, app, admin, q)
		assert.Equal(t, http.StatusUnprocessableEntity, status, q)
	}
}

// TestPaymentsExport_PagesNeitherSkipNorRepeatRows: an export longer than
// one page (exportBatchSize = 500) holds every payment exactly once, in
// paid_at then id order — paid_at runs against id order here.
func TestPaymentsExport_PagesNeitherSkipNorRepeatRows(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	const n = 1103
	payments := make([]models.Payment, n)
	base := localDay(2026, 1, 1)
	for i := range payments {
		// Later ids get earlier days; every 3 share a day, so id breaks ties.
		payments[i] = models.Payment{DealID: deal.ID, Amount: float64(i + 1), Method: models.PaymentMethodCash,
			PaidAt: base.AddDate(0, 0, (n-i)/3)}
	}
	require.NoError(t, db.CreateInBatches(&payments, 500).Error)

	status, rows, _ := exportPayments(t, app, admin, "")
	require.Equal(t, http.StatusOK, status)
	require.Len(t, rows, n+1, "header + every payment")
	seen := map[string]bool{}
	for _, r := range rows[1:] {
		assert.False(t, seen[r[5]], "amount %s exported twice", r[5])
		seen[r[5]] = true
	}
	for i := 2; i < len(rows); i++ {
		assert.LessOrEqual(t, rows[i-1][0], rows[i][0], "paid_at ascending at row %d", i)
	}
}

package apitests

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// --- Payments: WHT / 50 ทวิ / FlowAccount document number / installment link ---

func TestPayment_TaxFieldsRoundTripAndPartialUpdate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	// The payment below settles 10,000; the receivable must cover it.
	require.NoError(t, db.Model(deal).Update("value", 10000).Error)
	inst := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 0, -3)}
	require.NoError(t, db.Create(inst).Error)

	var created struct {
		Data models.Payment `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payments", map[string]interface{}{
		"amount": 9700, "method": "transfer", "wht_amount": 300, "document_number": " RE2026090001 ", "installment_id": inst.ID,
	}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.InDelta(t, 300, created.Data.WhtAmount, 0.001)
	assert.False(t, created.Data.WhtCertificateReceived)
	require.NotNil(t, created.Data.DocumentNumber)
	assert.Equal(t, "RE2026090001", *created.Data.DocumentNumber)
	require.NotNil(t, created.Data.InstallmentID)

	// Installment settled by cash + WHT.
	var schedule struct {
		Data []utils.InstallmentStatus `json:"data"`
	}
	doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payment-installments", nil, admin.ID, admin.Role), &schedule)
	require.Len(t, schedule.Data, 1)
	assert.Equal(t, utils.InstallmentStatusPaid, schedule.Data[0].Status)

	// Partial update: only the certificate flag changes.
	var updated struct {
		Data models.Payment `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(created.Data.ID), map[string]interface{}{
		"wht_certificate_received": true,
	}, admin.ID, admin.Role), &updated)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, updated.Data.WhtCertificateReceived)
	assert.InDelta(t, 9700, updated.Data.Amount, 0.001)
	assert.InDelta(t, 300, updated.Data.WhtAmount, 0.001)
	require.NotNil(t, updated.Data.InstallmentID, "omitted installment_id keeps the link")

	// Explicit null unlinks.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/payments/"+itoa(created.Data.ID), map[string]interface{}{
		"installment_id": nil,
	}, admin.ID, admin.Role), &updated)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, updated.Data.InstallmentID)

	var list struct {
		Data struct {
			TotalPaid    float64 `json:"total_paid"`
			TotalWht     float64 `json:"total_wht"`
			TotalSettled float64 `json:"total_settled"`
		} `json:"data"`
	}
	doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/deals/"+itoa(deal.ID)+"/payments", nil, admin.ID, admin.Role), &list)
	assert.InDelta(t, 9700, list.Data.TotalPaid, 0.001)
	assert.InDelta(t, 300, list.Data.TotalWht, 0.001)
	assert.InDelta(t, 10000, list.Data.TotalSettled, 0.001)
}

func TestPayment_RejectsInstallmentFromAnotherDealAndNegativeWht(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	other := seedDeal(t, db, nil)
	foreign := &models.PaymentInstallment{DealID: other.ID, Amount: 1000, DueDate: time.Now()}
	require.NoError(t, db.Create(foreign).Error)

	for name, body := range map[string]map[string]interface{}{
		"foreign installment": {"amount": 100, "installment_id": foreign.ID},
		"missing installment": {"amount": 100, "installment_id": 999999},
		"negative wht":        {"amount": 100, "wht_amount": -1},
		"missing amount":      {"wht_amount": 10},
	} {
		resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/payments", body, admin.ID, admin.Role), nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, name)
	}
}

func TestPaymentInstallmentDelete_UnlinksPayments(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	inst := &models.PaymentInstallment{DealID: deal.ID, Amount: 1000, DueDate: time.Now()}
	require.NoError(t, db.Create(inst).Error)
	payment := &models.Payment{DealID: deal.ID, Amount: 1000, PaidAt: time.Now(), InstallmentID: &inst.ID}
	require.NoError(t, db.Create(payment).Error)

	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodDelete, "/api/v1/payment-installments/"+itoa(inst.ID), nil, admin.ID, admin.Role), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	var reloaded models.Payment
	require.NoError(t, db.First(&reloaded, payment.ID).Error)
	assert.Nil(t, reloaded.InstallmentID)
}

// --- Outstanding balance: receivable from the Accepted Quote incl. VAT ---

func TestOutstandingBalance_ReceivableFromAcceptedQuote(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(deal).UpdateColumns(map[string]interface{}{"status": models.DealStatusWon, "value": 100000}).Error)
	// An older accepted quote and a newer one: the newer wins.
	require.NoError(t, db.Create(&models.Quote{DealID: deal.ID, Status: models.QuoteStatusAccepted, VatEnabled: true,
		Items: models.JSONItems{{Description: "Old", Qty: 1, Price: 50000}}}).Error)
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, db.Create(&models.Quote{DealID: deal.ID, Status: models.QuoteStatusAccepted, VatEnabled: true,
		WhtEnabled: true, WhtRate: 3, Items: models.JSONItems{{Description: "Build", Qty: 1, Price: 100000}}}).Error)
	// A draft quote is ignored.
	require.NoError(t, db.Create(&models.Quote{DealID: deal.ID, Status: models.QuoteStatusDraft, VatEnabled: true,
		Items: models.JSONItems{{Description: "Draft", Qty: 1, Price: 999999}}}).Error)
	require.NoError(t, db.Create(&models.Payment{DealID: deal.ID, Amount: 48500, WhtAmount: 1500, PaidAt: time.Now()}).Error)
	require.NoError(t, db.Create(&models.PaymentInstallment{DealID: deal.ID, Amount: 50000, DueDate: time.Now().AddDate(0, 0, -100)}).Error)
	require.NoError(t, db.Create(&models.PaymentInstallment{DealID: deal.ID, Amount: 57000, DueDate: time.Now().AddDate(0, 0, -40)}).Error)

	var out struct {
		Data []struct {
			DealID               uint       `json:"deal_id"`
			ReceivableAmount     float64    `json:"receivable_amount"`
			ReceivableSource     string     `json:"receivable_source"`
			PaidAmount           float64    `json:"paid_amount"`
			WhtAmount            float64    `json:"wht_amount"`
			OutstandingAmount    float64    `json:"outstanding_amount"`
			Aging                string     `json:"aging"`
			OldestOverdueDueDate *time.Time `json:"oldest_overdue_due_date"`
			DaysOverdue          int        `json:"days_overdue"`
			AgingBucket          string     `json:"aging_bucket"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/outstanding-balance", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 1)
	row := out.Data[0]
	assert.Equal(t, "quote", row.ReceivableSource)
	assert.InDelta(t, 107000, row.ReceivableAmount, 0.001, "100,000 + 7% VAT, before WHT")
	assert.InDelta(t, 48500, row.PaidAmount, 0.001)
	assert.InDelta(t, 1500, row.WhtAmount, 0.001)
	assert.InDelta(t, 57000, row.OutstandingAmount, 0.001)
	assert.Equal(t, "overdue", row.Aging)
	require.NotNil(t, row.OldestOverdueDueDate)
	assert.Equal(t, 40, row.DaysOverdue, "installment #1 is covered, #2 is 40 days late")
	assert.Equal(t, "31_60", row.AgingBucket)

	csvResp, err := app.Test(testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/outstanding-balance/export", nil, admin.ID, admin.Role))
	require.NoError(t, err)
	body, _ := io.ReadAll(csvResp.Body)
	assert.True(t, strings.HasPrefix(string(body), "Deal,Company,Deal Value,Paid,Outstanding,Aging,Receivable,"), string(body))
}

// --- Duplicate quote ---

func TestQuoteDuplicate_CopiesAsNewDraft(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDeal(t, db, &rep.ID)
	issue, validity, notes, ref := "2026-01-01", "2026-01-31", "Pay within 30 days", "PO-1"
	number := "QT2026010001"
	now := time.Now()
	fileURL := "/uploads/x.pdf"
	src := &models.Quote{DealID: deal.ID, Number: &number, Status: models.QuoteStatusAccepted,
		Items:       models.JSONItems{{Description: "ค่าพัฒนาระบบ", Qty: 2, Price: 5000, DiscountPercent: 10}},
		ScopeOfWork: "Phase 1", IssueDate: &issue, ValidityDate: &validity, CreditDays: 30,
		PriceType: models.QuotePriceTypeInclTax, WhtEnabled: true, WhtRate: 3, DiscountTotal: 500,
		Notes: &notes, ReferenceNumber: &ref, FileURL: &fileURL, UploadedAt: &now}
	require.NoError(t, db.Create(src).Error)
	require.NoError(t, db.Model(src).UpdateColumn("vat_enabled", false).Error)

	var out struct {
		Data models.Quote `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/quotes/"+itoa(src.ID)+"/duplicate", nil, rep.ID, rep.Role), &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	dup := out.Data
	assert.NotEqual(t, src.ID, dup.ID)
	assert.Equal(t, deal.ID, dup.DealID)
	assert.Equal(t, models.QuoteStatusDraft, dup.Status)
	require.NotNil(t, dup.Number)
	assert.NotEqual(t, number, *dup.Number)
	assert.True(t, strings.HasPrefix(*dup.Number, "QT"))
	assert.Equal(t, src.Items, dup.Items)
	assert.Equal(t, "Phase 1", dup.ScopeOfWork)
	assert.Equal(t, 30, dup.CreditDays)
	assert.Equal(t, models.QuotePriceTypeInclTax, dup.PriceType)
	assert.InDelta(t, 3, dup.WhtRate, 0.001)
	assert.InDelta(t, 500, dup.DiscountTotal, 0.001)
	assert.Nil(t, dup.FileURL, "uploaded file is not copied")
	today := time.Now().Format("2006-01-02")
	require.NotNil(t, dup.IssueDate)
	assert.Equal(t, today, *dup.IssueDate)
	require.NotNil(t, dup.ValidityDate)
	assert.Equal(t, time.Now().AddDate(0, 0, 30).Format("2006-01-02"), *dup.ValidityDate)

	var stored models.Quote
	require.NoError(t, db.First(&stored, dup.ID).Error)
	assert.False(t, stored.VatEnabled, "a no-VAT original stays no-VAT")
	assert.False(t, dup.VatEnabled, "and the response says so")

	// Another rep can't duplicate a quote on a deal they don't own.
	other := testutil.CreateUser(t, db, models.RoleSalesRep)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/quotes/"+itoa(src.ID)+"/duplicate", nil, other.ID, other.Role), nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/quotes/"+itoa(src.ID)+"/duplicate", nil, production.ID, production.Role), nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/quotes/999999/duplicate", nil, rep.ID, rep.Role), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// POST /deals/:id/quotes with vat_enabled:false used to be stored as true
// (GORM drops a false defaulted column from the INSERT), so the quote — and
// now the receivable derived from it — silently gained 7% VAT.
func TestQuoteCreate_VatDisabledIsStored(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	var out struct {
		Data models.Quote `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/quotes", map[string]interface{}{
		"vat_enabled": false, "items": []map[string]interface{}{{"description": "x", "qty": 1, "price": 100}},
	}, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.False(t, out.Data.VatEnabled)
	var stored models.Quote
	require.NoError(t, db.First(&stored, out.Data.ID).Error)
	assert.False(t, stored.VatEnabled)
}

// --- Source performance ---

func TestSourcePerformance_FollowsLeadsToWonDeals(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	mkLead := func(source models.LeadSource, status models.LeadStatus) *models.Lead {
		lead := &models.Lead{Name: "L", Source: source, Status: status}
		require.NoError(t, db.Create(lead).Error)
		return lead
	}
	mkDeal := func(channel models.LeadSource, status models.DealStatus, value float64, leadID *uint) *models.Deal {
		d := seedDeal(t, db, nil)
		require.NoError(t, db.Model(d).UpdateColumns(map[string]interface{}{"channel": channel, "status": status, "value": value, "lead_id": leadID}).Error)
		return d
	}

	// Website: 3 leads, 2 qualified, 1 converted into a Won deal whose channel
	// was later edited to "Ads" — must still count for Website.
	mkLead(models.LeadSourceWebsite, models.LeadStatusNew)
	mkLead(models.LeadSourceWebsite, models.LeadStatusQualified)
	won := mkLead(models.LeadSourceWebsite, models.LeadStatusQualified)
	wonDeal := mkDeal(models.LeadSourceAds, models.DealStatusWon, 50000, &won.ID)
	require.NoError(t, db.Model(won).UpdateColumn("converted_deal_id", wonDeal.ID).Error)
	// Referral: 1 lead converted into a Lost deal (linked only via converted_deal_id).
	lost := mkLead(models.LeadSourceReferral, models.LeadStatusQualified)
	lostDeal := mkDeal(models.LeadSourceReferral, models.DealStatusLost, 10000, nil)
	require.NoError(t, db.Model(lost).UpdateColumn("converted_deal_id", lostDeal.ID).Error)
	// Direct Won deal (no lead) on channel Event.
	mkDeal(models.LeadSourceEvent, models.DealStatusWon, 20000, nil)
	// A lead from last year is outside the window.
	old := mkLead(models.LeadSourceWebsite, models.LeadStatusQualified)
	require.NoError(t, db.Model(old).UpdateColumn("created_at", time.Now().AddDate(-1, 0, 0)).Error)

	today := time.Now().Format("2006-01-02")
	from := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	var out struct {
		Data []struct {
			Source         string  `json:"source"`
			Leads          int64   `json:"leads"`
			Qualified      int64   `json:"qualified"`
			DealsWon       int64   `json:"deals_won"`
			WonValue       float64 `json:"won_value"`
			WinRate        float64 `json:"win_rate"`
			DirectDealsWon int64   `json:"direct_deals_won"`
			DirectWonValue float64 `json:"direct_won_value"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/source-performance?date_from="+from+"&date_to="+today, nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	bySource := map[string]int{}
	for i, r := range out.Data {
		bySource[r.Source] = i
	}
	require.Contains(t, bySource, "Website")
	web := out.Data[bySource["Website"]]
	assert.EqualValues(t, 3, web.Leads, "date_to is inclusive; last year's lead excluded")
	assert.EqualValues(t, 2, web.Qualified)
	assert.EqualValues(t, 1, web.DealsWon)
	assert.InDelta(t, 50000, web.WonValue, 0.001)
	assert.InDelta(t, 100.0/3, web.WinRate, 0.01)
	ref := out.Data[bySource["Referral"]]
	assert.EqualValues(t, 1, ref.Leads)
	assert.EqualValues(t, 0, ref.DealsWon)
	require.Contains(t, bySource, "Event")
	assert.EqualValues(t, 1, out.Data[bySource["Event"]].DirectDealsWon)
	assert.InDelta(t, 20000, out.Data[bySource["Event"]].DirectWonValue, 0.001)
	assert.NotContains(t, bySource, "Ads", "the won deal counts under its lead's source, not its edited channel")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/source-performance?date_from=nope", nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/source-performance?from="+today+"&to="+from, nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "reversed range")

	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/source-performance", nil, rep.ID, rep.Role), nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "same Admin/Sales Manager gate as sibling reports")

	csvResp, err := app.Test(testutil.AuthRequest(t, http.MethodGet, "/api/v1/reports/source-performance/export", nil, admin.ID, admin.Role))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, csvResp.StatusCode)
	body, _ := io.ReadAll(csvResp.Body)
	assert.True(t, strings.HasPrefix(string(body), "Source,Leads,Qualified,Deals Won,Won Value,Win Rate (%)"), string(body))
}

// --- Notification rule create_task, renewal entity types; contract end_date; renewal fields ---

func TestNotificationRule_CreateTaskFlagAndNewEntityTypes(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	var created struct {
		Data models.NotificationRule `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/notification-rules", map[string]interface{}{
		"name": "Renewals", "entity_type": "customer_product_renewal", "threshold_days": 30, "recipient_role": "owner",
	}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.True(t, created.Data.CreateTask, "create_task defaults to true")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/notification-rules", map[string]interface{}{
		"name": "Expiry, email only", "entity_type": "contract_expiry", "threshold_days": 14, "recipient_role": "owner",
		"create_task": false, "is_active": false,
	}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var stored models.NotificationRule
	require.NoError(t, db.First(&stored, created.Data.ID).Error)
	assert.False(t, stored.CreateTask, "an explicit false must survive the column default")
	assert.False(t, stored.IsActive, "an explicit false must survive the column default")

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/admin/notification-rules/"+itoa(stored.ID), map[string]interface{}{
		"name": stored.Name, "entity_type": "contract_expiry", "threshold_days": 14, "recipient_role": "owner", "create_task": true,
	}, admin.ID, admin.Role), &created)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, created.Data.CreateTask)
}

func TestContractEndDateAndCustomerProductRenewalFields(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)

	var contract struct {
		Data models.Contract `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/deals/"+itoa(deal.ID)+"/contracts", map[string]interface{}{
		"status": "signed", "end_date": "2027-03-31",
	}, admin.ID, admin.Role), &contract)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, contract.Data.EndDate)
	assert.Equal(t, "2027-03-31", contract.Data.EndDate.Format("2006-01-02"))

	// Update without end_date keeps it; null clears it; garbage is a 422.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.Data.ID), map[string]interface{}{"status": "signed"}, admin.ID, admin.Role), &contract)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, contract.Data.EndDate)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.Data.ID), map[string]interface{}{"end_date": "31/03/2027"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPut, "/api/v1/contracts/"+itoa(contract.Data.ID), map[string]interface{}{"end_date": nil}, admin.ID, admin.Role), &contract)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, contract.Data.EndDate)

	product := &models.Product{Name: "CRM Cloud", Price: 1000, IsActive: true}
	require.NoError(t, db.Create(product).Error)
	var cp struct {
		Data models.CustomerProduct `json:"data"`
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies/"+itoa(deal.CompanyID)+"/products", map[string]interface{}{
		"product_id": product.ID, "status": "Active", "renewal_date": "2027-01-15T00:00:00+07:00", "billing_cycle": "yearly", "price": 12000,
	}, admin.ID, admin.Role), &cp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, cp.Data.RenewalDate)
	assert.Equal(t, "2027-01-15", cp.Data.RenewalDate.Format("2006-01-02"), "the date as picked, not shifted to UTC")
	require.NotNil(t, cp.Data.BillingCycle)
	assert.Equal(t, models.BillingCycleYearly, *cp.Data.BillingCycle)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/customer-products/"+itoa(cp.Data.ID), map[string]interface{}{
		"renewal_date": "2028-01-15",
	}, admin.ID, admin.Role), &cp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "2028-01-15", cp.Data.RenewalDate.Format("2006-01-02"))
	require.NotNil(t, cp.Data.Price, "omitted price is kept")
	assert.InDelta(t, 12000, *cp.Data.Price, 0.001)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/customer-products/"+itoa(cp.Data.ID), map[string]interface{}{
		"billing_cycle": "weekly",
	}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// Customer-product end_date: a bad value is a 422 (it used to be dropped
// silently with a 200), the frontend's toISOString instant is stored as
// sent, a bare date is Bangkok midnight, null clears, and Create applies it
// (it used to ignore end_date altogether).
func TestCustomerProduct_EndDate(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	product := &models.Product{Name: "CRM Cloud", Price: 1000, IsActive: true}
	require.NoError(t, db.Create(product).Error)
	var cp struct {
		Data models.CustomerProduct `json:"data"`
	}

	// 1 Oct 2026 00:00 Bangkok, as the frontend serializes it.
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies/"+itoa(company.ID)+"/products", map[string]interface{}{
		"product_id": product.ID, "status": "Active", "end_date": "2026-09-30T17:00:00.000Z",
	}, admin.ID, admin.Role), &cp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, cp.Data.EndDate, "Create applies end_date")
	assert.True(t, cp.Data.EndDate.Equal(time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)), "stored as the instant sent, got %s", cp.Data.EndDate)

	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies/"+itoa(company.ID)+"/products", map[string]interface{}{
		"product_id": product.ID, "status": "Active", "end_date": "soon",
	}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	patch := func(body map[string]interface{}) int {
		return doJSON(t, app, testutil.AuthRequest(t, http.MethodPatch, "/api/v1/customer-products/"+itoa(cp.Data.ID), body, admin.ID, admin.Role), &cp).StatusCode
	}
	assert.Equal(t, http.StatusUnprocessableEntity, patch(map[string]interface{}{"status": "Churned", "end_date": "31/12/2026"}))
	var stored models.CustomerProduct
	require.NoError(t, db.First(&stored, cp.Data.ID).Error)
	assert.Equal(t, models.CustomerProductActive, stored.Status, "a rejected PATCH changes nothing")

	require.Equal(t, http.StatusOK, patch(map[string]interface{}{"status": "Churned", "end_date": "2026-12-31"}))
	require.NotNil(t, cp.Data.EndDate)
	assert.True(t, cp.Data.EndDate.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local)), "got %s", cp.Data.EndDate)

	require.Equal(t, http.StatusOK, patch(map[string]interface{}{"status": "Churned", "end_date": nil}))
	assert.Nil(t, cp.Data.EndDate)
}

// Rule-created Tasks show up in-app: the notification log lists the new
// entity types resolved to their Deal/Company.
func TestNotificationLog_ResolvesInstallmentAndRenewalFirings(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	inst := &models.PaymentInstallment{DealID: deal.ID, Amount: 1, DueDate: time.Now()}
	require.NoError(t, db.Create(inst).Error)
	product := &models.Product{Name: "P", IsActive: true}
	require.NoError(t, db.Create(product).Error)
	cp := &models.CustomerProduct{CompanyID: deal.CompanyID, ProductID: product.ID, StartDate: time.Now()}
	require.NoError(t, db.Create(cp).Error)

	instRule := models.NotificationRule{Name: "i", EntityType: models.NotificationEntityPaymentInstallment, ThresholdDays: 1, RecipientRole: models.NotificationRecipientOwner, IsActive: true}
	renRule := models.NotificationRule{Name: "r", EntityType: models.NotificationEntityCustomerProductRenewal, ThresholdDays: 1, RecipientRole: models.NotificationRecipientOwner, IsActive: true}
	require.NoError(t, db.Create(&instRule).Error)
	require.NoError(t, db.Create(&renRule).Error)
	require.NoError(t, db.Create(&models.NotificationLog{RuleID: instRule.ID, EntityID: inst.ID, NotifiedAt: time.Now()}).Error)
	require.NoError(t, db.Create(&models.NotificationLog{RuleID: renRule.ID, EntityID: cp.ID, Context: "2027-01-15", NotifiedAt: time.Now()}).Error)

	var out struct {
		Data []struct {
			EntityType string `json:"entity_type"`
			DealID     uint   `json:"deal_id"`
			CompanyID  uint   `json:"company_id"`
			EntityID   uint   `json:"entity_id"`
			Context    string `json:"context"`
		} `json:"data"`
	}
	resp := doJSON(t, app, testutil.AuthRequest(t, http.MethodGet, "/api/v1/notification-log", nil, admin.ID, admin.Role), &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	byType := map[string]int{}
	for i, r := range out.Data {
		byType[r.EntityType] = i
	}
	require.Contains(t, byType, "payment_installment")
	assert.Equal(t, deal.ID, out.Data[byType["payment_installment"]].DealID)
	assert.Equal(t, inst.ID, out.Data[byType["payment_installment"]].EntityID)
	require.Contains(t, byType, "customer_product_renewal")
	assert.Equal(t, deal.CompanyID, out.Data[byType["customer_product_renewal"]].CompanyID)
	assert.Equal(t, "2027-01-15", out.Data[byType["customer_product_renewal"]].Context)
}

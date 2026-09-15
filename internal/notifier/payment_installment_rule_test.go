package notifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

func seedInstallmentRule(t *testing.T, db *gorm.DB, thresholdDays int) models.NotificationRule {
	t.Helper()
	rule := models.NotificationRule{
		Name:          "Installment due test rule " + time.Now().Format("150405.000000000"),
		EntityType:    models.NotificationEntityPaymentInstallment,
		ThresholdDays: thresholdDays,
		RecipientRole: models.NotificationRecipientOwner,
		IsActive:      true,
	}
	require.NoError(t, db.Create(&rule).Error)
	return rule
}

// TestCheckPaymentInstallmentDueRule_FiresWithinThreshold guards the core
// condition: an unpaid installment due within ThresholdDays gets notified,
// one not yet within that window doesn't.
func TestCheckPaymentInstallmentDueRule_FiresWithinThreshold(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)

	dueSoon := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 0, 3)}
	require.NoError(t, db.Create(dueSoon).Error)
	dueFar := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 0, 60)}
	require.NoError(t, db.Create(dueFar).Error)

	rule := seedInstallmentRule(t, db, 7)
	checkPaymentInstallmentDueRule(db, testutil.Config(), rule)

	require.True(t, alreadyNotified(db, rule.ID, dueSoon.ID, ""), "an installment due within the threshold must be notified")
	require.False(t, alreadyNotified(db, rule.ID, dueFar.ID, ""), "an installment due well outside the threshold must not be notified")
}

// TestCheckPaymentInstallmentDueRule_SkipsFullyPaid guards that a fully
// waterfall-covered installment (per utils.ComputeInstallmentStatuses) never
// fires, even if its due date has passed.
func TestCheckPaymentInstallmentDueRule_SkipsFullyPaid(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)

	paid := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 0, -5)}
	require.NoError(t, db.Create(paid).Error)
	require.NoError(t, db.Create(&models.Payment{DealID: deal.ID, Amount: 10000, PaidAt: time.Now(), Method: models.PaymentMethodTransfer}).Error)

	rule := seedInstallmentRule(t, db, 7)
	checkPaymentInstallmentDueRule(db, testutil.Config(), rule)

	require.False(t, alreadyNotified(db, rule.ID, paid.ID, ""), "a fully paid installment must not be notified even if overdue")
}

// TestCheckPaymentInstallmentDueRule_FiresOnceEver guards the dedup
// behavior — same "fires once ever per entity" shape as Contract/Quote's own
// rules (empty Context), confirmed by running the check twice.
func TestCheckPaymentInstallmentDueRule_FiresOnceEver(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	overdue := &models.PaymentInstallment{DealID: deal.ID, Amount: 10000, DueDate: time.Now().AddDate(0, 0, -5)}
	require.NoError(t, db.Create(overdue).Error)

	rule := seedInstallmentRule(t, db, 7)
	checkPaymentInstallmentDueRule(db, testutil.Config(), rule)
	require.True(t, alreadyNotified(db, rule.ID, overdue.ID, ""))

	var count int64
	db.Model(&models.NotificationLog{}).Where("rule_id = ? AND entity_id = ?", rule.ID, overdue.ID).Count(&count)

	checkPaymentInstallmentDueRule(db, testutil.Config(), rule)
	var countAfter int64
	db.Model(&models.NotificationLog{}).Where("rule_id = ? AND entity_id = ?", rule.ID, overdue.ID).Count(&countAfter)
	require.Equal(t, count, countAfter, "a second run must not write a duplicate NotificationLog row")
}

// seedDealForNotifier is a minimal Deal seed local to this package's tests —
// mirrors tests/common_test.go's seedDeal shape (Company+Contact+Deal), which
// this package can't import directly (it's an internal package, not the
// tests/apitests one).
func seedDealForNotifier(t *testing.T, db *gorm.DB, assignedTo *uint) *models.Deal {
	t.Helper()
	company := &models.Company{Name: "Notifier Test Co", Status: models.StatusActive}
	require.NoError(t, db.Create(company).Error)
	contact := &models.Contact{CompanyID: company.ID, Name: "Notifier Contact", Status: models.StatusActive}
	require.NoError(t, db.Create(contact).Error)
	deal := &models.Deal{
		CompanyID: company.ID, ContactID: contact.ID, Title: "Notifier Test Deal",
		Value: 10000, Stage: models.DealStageLead, Status: models.DealStatusWon, AssignedTo: assignedTo,
	}
	require.NoError(t, db.Create(deal).Error)
	return deal
}

package apitests

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestBackfillInstallmentAlertContexts: legacy "" installment log rows take
// the state they fired in, other rule types are untouched, and it runs once.
func TestBackfillInstallmentAlertContexts(t *testing.T) {
	_, db := testutil.App(t)
	require.NoError(t, db.Where("name = ?", "installment_alert_contexts_backfill").Delete(&models.DataMigration{}).Error)

	deal := seedDeal(t, db, nil)
	now := time.Now()
	early := &models.PaymentInstallment{DealID: deal.ID, Amount: 1000, DueDate: now.AddDate(0, 0, 3)}
	late := &models.PaymentInstallment{DealID: deal.ID, Amount: 1000, DueDate: now.AddDate(0, 0, -3)}
	require.NoError(t, db.Create(early).Error)
	require.NoError(t, db.Create(late).Error)

	instRule := models.NotificationRule{Name: "Backfill inst", EntityType: models.NotificationEntityPaymentInstallment, ThresholdDays: 7, RecipientRole: models.NotificationRecipientOwner}
	quoteRule := models.NotificationRule{Name: "Backfill quote", EntityType: models.NotificationEntityQuote, ThresholdDays: 7, RecipientRole: models.NotificationRecipientOwner}
	require.NoError(t, db.Create(&instRule).Error)
	require.NoError(t, db.Create(&quoteRule).Error)
	logs := []models.NotificationLog{
		{RuleID: instRule.ID, EntityID: early.ID, NotifiedAt: now},
		{RuleID: instRule.ID, EntityID: late.ID, NotifiedAt: now},
		{RuleID: quoteRule.ID, EntityID: early.ID, NotifiedAt: now},
	}
	require.NoError(t, db.Create(&logs).Error)

	require.NoError(t, database.BackfillInstallmentAlertContexts(db))
	context := func(id uint) string {
		var l models.NotificationLog
		require.NoError(t, db.First(&l, id).Error)
		return l.Context
	}
	assert.Equal(t, "due_soon", context(logs[0].ID))
	assert.Equal(t, "overdue", context(logs[1].ID))
	assert.Equal(t, "", context(logs[2].ID), "other rule types keep their empty context")

	require.NoError(t, db.Model(&models.NotificationLog{}).Where("id = ?", logs[0].ID).UpdateColumn("context", "").Error)
	require.NoError(t, database.BackfillInstallmentAlertContexts(db))
	assert.Equal(t, "", context(logs[0].ID), "second run is a no-op")
}

package apitests

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// CreateKeepingFalse stores an explicit false on a NOT NULL DEFAULT true
// column (a plain Create writes true), and leaves true alone. (Callers must set every
// such bool deliberately: an unset one is false too.)
func TestCreateKeepingFalse_PersistsExplicitFalse(t *testing.T) {
	_, db := testutil.App(t)

	off := models.NotificationRule{Name: "Off", EntityType: models.NotificationEntityQuote, ThresholdDays: 3,
		RecipientRole: models.NotificationRecipientOwner, IsActive: false, CreateTask: false}
	require.NoError(t, utils.CreateKeepingFalse(db, &off))
	assert.False(t, off.IsActive)
	assert.False(t, off.CreateTask)
	var stored models.NotificationRule
	require.NoError(t, db.First(&stored, off.ID).Error)
	assert.False(t, stored.IsActive)
	assert.False(t, stored.CreateTask)

	on := models.NotificationRule{Name: "On", EntityType: models.NotificationEntityQuote, ThresholdDays: 3, RecipientRole: models.NotificationRecipientOwner, IsActive: true}
	require.NoError(t, utils.CreateKeepingFalse(db, &on))
	var storedOn models.NotificationRule
	require.NoError(t, db.First(&storedOn, on.ID).Error)
	assert.True(t, storedOn.IsActive)
	assert.False(t, storedOn.CreateTask, "an unset bool is explicit false too")
}

// The insert and the write-back of false are one transaction: if the
// second statement fails, no rule is left behind active (the old
// Create-then-UpdateColumns returned 500 with the row already committed).
func TestNotificationRuleCreate_FailedFalseWriteLeavesNoRow(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	const cb = "test:fail_notification_rule_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "notification_rules" {
			_ = tx.AddError(errors.New("forced update failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(cb) })

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/admin/notification-rules", map[string]interface{}{
		"name": "Inactive rule", "entity_type": "quote", "threshold_days": 3, "recipient_role": "owner", "is_active": false,
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.NotEqual(t, fiber.StatusCreated, resp.StatusCode)

	var count int64
	require.NoError(t, db.Model(&models.NotificationRule{}).Where("name = ?", "Inactive rule").Count(&count).Error)
	assert.Zero(t, count, "a failed create must not leave an active rule behind")
}

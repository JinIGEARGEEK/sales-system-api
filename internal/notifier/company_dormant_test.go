package notifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// A soft-deleted Company (e.g. a duplicate merged into another) never fires
// the dormant-company rule; the query reads companies through db.Table, which
// skips GORM's soft-delete scope.
func TestCompanyDormantRule_SkipsDeletedCompany(t *testing.T) {
	_, db := testutil.App(t)
	testutil.CreateUser(t, db, models.RoleSalesManager)
	live := models.Company{Name: "Live Co", Status: models.StatusActive}
	gone := models.Company{Name: "Gone Co", Status: models.StatusActive}
	require.NoError(t, db.Create(&live).Error)
	require.NoError(t, db.Create(&gone).Error)
	require.NoError(t, db.Delete(&gone).Error)
	rule := seedRule(t, db, models.NotificationEntityCompany, 60, true)

	checkCompanyDormantRule(db, testutil.Config(), rule, time.Now())

	var logged []uint
	require.NoError(t, db.Model(&models.NotificationLog{}).Where("rule_id = ?", rule.ID).Pluck("entity_id", &logged).Error)
	assert.Equal(t, []uint{live.ID}, logged)
}

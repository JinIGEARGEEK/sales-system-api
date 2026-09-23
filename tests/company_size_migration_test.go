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

// TestMigrateCompanySizeDefaults guards the one-time upgrade of a database
// seeded before 2026-09-22: old unit-less default sizes are renamed to their
// "… คน" names, Companies using them follow (so they still pass Create/
// Update's active-option check), "> 100 คน" is added, custom/unknown values
// are left alone, no Company's updated_at moves, and a second run changes
// nothing (so an Admin's later edits survive restarts).
func TestMigrateCompanySizeDefaults(t *testing.T) {
	_, db := testutil.App(t)

	for _, name := range []string{"1-10", "11-50", "51-200", "201-500", "501-1000", "1000+", "Startup"} {
		require.NoError(t, db.Create(&models.CompanySizeOption{Name: name, IsActive: true}).Error)
	}
	onOld := &models.Company{Name: "Old Size Co", Status: models.StatusActive, Size: "11-50"}
	onCustom := &models.Company{Name: "Custom Size Co", Status: models.StatusActive, Size: "Startup"}
	onUnknown := &models.Company{Name: "Unknown Size Co", Status: models.StatusActive, Size: "50-100"}
	for _, c := range []*models.Company{onOld, onCustom, onUnknown} {
		require.NoError(t, db.Create(c).Error)
	}
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	require.NoError(t, db.Model(onOld).UpdateColumn("updated_at", past).Error)

	require.NoError(t, database.MigrateCompanySizeDefaults(db))

	names := func() []string {
		var rows []models.CompanySizeOption
		require.NoError(t, db.Unscoped().Order("name").Find(&rows).Error)
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return out
	}
	assert.ElementsMatch(t, []string{"1-10 คน", "11-50 คน", "51-200 คน", "201-500 คน", "501-1000 คน", "1000+ คน", "> 100 คน", "Startup"}, names())

	reload := func(c *models.Company) models.Company {
		var out models.Company
		require.NoError(t, db.First(&out, c.ID).Error)
		return out
	}
	old := reload(onOld)
	assert.Equal(t, "11-50 คน", old.Size, "companies follow the renamed option")
	assert.WithinDuration(t, past, old.UpdatedAt, time.Second, "a data migration isn't a user edit")
	assert.Equal(t, "Startup", reload(onCustom).Size, "custom sizes are untouched")
	assert.Equal(t, "50-100", reload(onUnknown).Size, "values that were never a default are untouched")

	// An Admin renames a size afterwards; a restart must not undo it.
	require.NoError(t, db.Model(&models.CompanySizeOption{}).Where("name = ?", "1-10 คน").Update("name", "1-10").Error)
	require.NoError(t, database.MigrateCompanySizeDefaults(db))
	assert.Contains(t, names(), "1-10", "second run is a no-op")
}

// TestMigrateCompanySizeDefaults_EmptyTableLeftToSeed: a fresh database gets
// the full default list from the startup seed, not from this migration.
func TestMigrateCompanySizeDefaults_EmptyTableLeftToSeed(t *testing.T) {
	_, db := testutil.App(t)
	require.NoError(t, database.MigrateCompanySizeDefaults(db))
	var count int64
	require.NoError(t, db.Model(&models.CompanySizeOption{}).Unscoped().Count(&count).Error)
	assert.Zero(t, count)
}

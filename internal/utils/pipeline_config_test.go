// External test package (not `package utils`): internal/testutil imports
// internal/utils, so a same-package test file here can't import testutil
// without a cycle — same reason internal/middleware's DB-backed tests live
// in `middleware_test` instead of `middleware`.
package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// TestEnsureActiveIndustry_CreatesReactivatesAndIsIdempotent covers
// EnsureActiveIndustry's three paths directly: a brand-new name creates an
// active row, a deactivated row gets reactivated, and calling it again with
// a name that already resolves to an active row is a no-op (not a second
// insert, which name's uniqueIndex would reject anyway).
func TestEnsureActiveIndustry_CreatesReactivatesAndIsIdempotent(t *testing.T) {
	_, db := testutil.App(t)

	require.NoError(t, utils.EnsureActiveIndustry(db, ""), "empty name is a no-op")

	const name = "Aerospace"
	require.NoError(t, utils.EnsureActiveIndustry(db, name))
	var opt models.IndustryOption
	require.NoError(t, db.Where("name = ?", name).First(&opt).Error)
	assert.True(t, opt.IsActive)

	// Idempotent: calling again must not attempt a second insert (name has a
	// uniqueIndex) and must leave the same row active.
	require.NoError(t, utils.EnsureActiveIndustry(db, name))
	var count int64
	require.NoError(t, db.Model(&models.IndustryOption{}).Where("name = ?", name).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	require.NoError(t, db.Model(&opt).Update("is_active", false).Error)
	require.NoError(t, utils.EnsureActiveIndustry(db, name), "must reactivate rather than error or duplicate")
	require.NoError(t, db.Where("name = ?", name).First(&opt).Error)
	assert.True(t, opt.IsActive)
}

// Package notifier tests using a real Postgres test DB via internal/testutil
// — safe here (unlike internal/middleware) because internal/testutil does
// NOT import internal/notifier, so there's no import-cycle concern and this
// can stay `package notifier` to reach the unexported check*/alreadyNotified/
// recipientEmails helpers directly.
package notifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// ageProspect backdates a Prospect's UpdatedAt directly via UpdateColumn (no
// hooks, so gorm doesn't immediately re-stamp it to time.Now() the way a
// normal Update/Save would), simulating "hasn't been touched in N days".
func ageProspect(t *testing.T, db *gorm.DB, id uint, age time.Duration) {
	t.Helper()
	require.NoError(t, db.Model(&models.Prospect{}).Where("id = ?", id).
		UpdateColumn("updated_at", time.Now().Add(-age)).Error)
}

// snapshotProspectStages captures every ProspectStage row's mutable fields
// and returns a func that restores them — needed because internal/testutil's
// TruncateAll deliberately does NOT truncate prospect_stages (it's seeded
// once per test binary, not per test, see testutil.seedPipelineConfig), so a
// test that mutates is_disqualified_stage/name on the shared seeded rows must
// put them back or it corrupts every test that runs afterward in this same
// binary.
func snapshotProspectStages(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	var stages []models.ProspectStage
	require.NoError(t, db.Find(&stages).Error)
	originals := make([]models.ProspectStage, len(stages))
	copy(originals, stages)
	return func() {
		for _, s := range originals {
			require.NoError(t, db.Model(&models.ProspectStage{}).Where("id = ?", s.ID).
				Updates(map[string]interface{}{
					"name":                  s.Name,
					"is_disqualified_stage": s.IsDisqualifiedStage,
				}).Error)
		}
	}
}

func seedProspectRule(t *testing.T, db *gorm.DB, thresholdDays int, recipientRole models.NotificationRecipientRole) models.NotificationRule {
	t.Helper()
	rule := models.NotificationRule{
		Name:          "Stale prospect test rule " + time.Now().Format("150405.000000000"),
		EntityType:    models.NotificationEntityProspect,
		ThresholdDays: thresholdDays,
		RecipientRole: recipientRole,
		IsActive:      true,
	}
	require.NoError(t, db.Create(&rule).Error)
	return rule
}

func seedTestProspect(t *testing.T, db *gorm.DB, status models.ProspectStatus, assignedTo *uint) *models.Prospect {
	t.Helper()
	p := &models.Prospect{
		Name:       "Stale Test Prospect",
		Source:     "Social Media",
		Status:     status,
		AssignedTo: assignedTo,
	}
	require.NoError(t, db.Create(p).Error)
	return p
}

// --- checkProspectStaleRule: the "Disqualified-rename gap" regression (fixed
// in 4e713b7) — an Admin renaming the ProspectStage flagged
// IsDisqualifiedStage must NOT re-enable stale notifications for Prospects
// sitting in that (renamed) stage, since checkProspectStaleRule is supposed
// to resolve the exclusion by the flag, not by the hardcoded literal
// "Disqualified". ---

func TestCheckProspectStaleRule_DisqualifiedRenameGap(t *testing.T) {
	_, db := testutil.App(t)
	defer snapshotProspectStages(t, db)()

	// Rename the seeded default "Disqualified" ProspectStage (is_disqualified_stage
	// stays true) — this is exactly the scenario 4e713b7's commit message
	// describes: an Admin renaming the stage.
	const renamedTo = "Not Interested"
	require.NoError(t, db.Model(&models.ProspectStage{}).
		Where("is_disqualified_stage = ?", true).
		Update("name", renamedTo).Error)

	rule := seedProspectRule(t, db, 5, models.NotificationRecipientOwner)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)

	// A Prospect sitting in the renamed disqualified stage, stale well past
	// the threshold — must NOT be notified. Before the fix, the query
	// excluded only the literal "Disqualified", so this prospect (status ==
	// "Not Interested") would have wrongly matched and gotten notified.
	disqualified := seedTestProspect(t, db, models.ProspectStatus(renamedTo), &owner.ID)
	ageProspect(t, db, disqualified.ID, 10*24*time.Hour)

	// Control: a genuinely-stale, still-active Prospect must still be
	// notified — proves the rule isn't just vacuously excluding everything.
	active := seedTestProspect(t, db, models.ProspectStatusEngaging, &owner.ID)
	ageProspect(t, db, active.ID, 10*24*time.Hour)

	checkProspectStaleRule(db, testutil.Config(), rule)

	require.False(t, alreadyNotified(db, rule.ID, disqualified.ID, renamedTo),
		"a Prospect in the renamed disqualified stage must not be notified")
	require.True(t, alreadyNotified(db, rule.ID, active.ID, string(models.ProspectStatusEngaging)),
		"a still-active stale Prospect must be notified")
}

// TestCheckProspectStaleRule_FallsBackToLiteralWhenNoStageFlagged covers the
// documented fallback: if no ProspectStage row is flagged
// IsDisqualifiedStage at all (e.g. right after a migration, before the seed
// runs), the rule must fall back to the hardcoded "Disqualified" literal
// rather than exclude nothing.
func TestCheckProspectStaleRule_FallsBackToLiteralWhenNoStageFlagged(t *testing.T) {
	_, db := testutil.App(t)
	defer snapshotProspectStages(t, db)()

	require.NoError(t, db.Model(&models.ProspectStage{}).
		Where("is_disqualified_stage = ?", true).
		Update("is_disqualified_stage", false).Error)

	rule := seedProspectRule(t, db, 5, models.NotificationRecipientOwner)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)

	disqualified := seedTestProspect(t, db, models.ProspectStatusDisqualified, &owner.ID)
	ageProspect(t, db, disqualified.ID, 10*24*time.Hour)

	checkProspectStaleRule(db, testutil.Config(), rule)

	require.False(t, alreadyNotified(db, rule.ID, disqualified.ID, string(models.ProspectStatusDisqualified)),
		"with no ProspectStage flagged, the literal 'Disqualified' fallback must still exclude it")
}

// TestCheckProspectStaleRule_ExactlyOneFlaggedStageWins covers the
// terminal-stage-exclusivity invariant from 166c030: checkProspectStaleRule's
// lookup (`Where("is_disqualified_stage = ?", true).First(...)`) only makes
// sense — resolves one unambiguous name — when handlers enforce that at most
// one ProspectStage row can carry the flag at a time. This asserts the
// happy-path contract that invariant is supposed to guarantee: with exactly
// one row flagged, its exact (possibly custom) name is the one excluded, and
// no other stage name is treated as disqualified.
func TestCheckProspectStaleRule_ExactlyOneFlaggedStageWins(t *testing.T) {
	_, db := testutil.App(t)
	defer snapshotProspectStages(t, db)()

	const customName = "Archived"
	require.NoError(t, db.Model(&models.ProspectStage{}).
		Where("is_disqualified_stage = ?", true).
		Update("name", customName).Error)

	var flaggedCount int64
	require.NoError(t, db.Model(&models.ProspectStage{}).
		Where("is_disqualified_stage = ?", true).Count(&flaggedCount).Error)
	require.EqualValues(t, 1, flaggedCount, "exactly one ProspectStage should carry the flag")

	rule := seedProspectRule(t, db, 5, models.NotificationRecipientOwner)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)

	// A Prospect that happens to carry the *old* literal name "Disqualified"
	// as a free-form status value (never actually re-flagged) should NOT be
	// treated as disqualified anymore — only the flagged row's current name
	// ("Archived") is excluded.
	staleUnderOldLiteral := seedTestProspect(t, db, models.ProspectStatusDisqualified, &owner.ID)
	ageProspect(t, db, staleUnderOldLiteral.ID, 10*24*time.Hour)

	staleUnderFlaggedName := seedTestProspect(t, db, models.ProspectStatus(customName), &owner.ID)
	ageProspect(t, db, staleUnderFlaggedName.ID, 10*24*time.Hour)

	checkProspectStaleRule(db, testutil.Config(), rule)

	require.True(t, alreadyNotified(db, rule.ID, staleUnderOldLiteral.ID, string(models.ProspectStatusDisqualified)),
		"a Prospect merely sharing the old literal name text is not the flagged stage and should be notified")
	require.False(t, alreadyNotified(db, rule.ID, staleUnderFlaggedName.ID, customName),
		"a Prospect in the currently-flagged stage must be excluded regardless of its name")
}

// --- alreadyNotified / recordNotified dedup logic ---

func TestAlreadyNotifiedAndRecordNotified(t *testing.T) {
	_, db := testutil.App(t)

	rule := seedProspectRule(t, db, 5, models.NotificationRecipientOwner)
	const entityID = uint(777)
	const context = "Engaging"

	require.False(t, alreadyNotified(db, rule.ID, entityID, context), "nothing recorded yet")

	require.NoError(t, recordNotified(db, rule.ID, entityID, context))
	require.True(t, alreadyNotified(db, rule.ID, entityID, context), "must dedup within the same context")

	// A different context (e.g. the entity moved to a new stage/status) is a
	// distinct idempotency key and must still be eligible to notify.
	require.False(t, alreadyNotified(db, rule.ID, entityID, "Nurturing"),
		"a different context must not be suppressed by an earlier notification")

	// A different rule entirely is also a distinct key even with the same
	// entity+context.
	otherRule := seedProspectRule(t, db, 5, models.NotificationRecipientOwner)
	require.False(t, alreadyNotified(db, otherRule.ID, entityID, context),
		"a different rule must not share dedup state with another rule")
}

// --- recipientEmails owner vs owner_and_managers resolution ---

func TestRecipientEmails_OwnerOnly(t *testing.T) {
	_, db := testutil.App(t)

	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	_ = manager

	emails := recipientEmails(db, &owner.ID, models.NotificationRecipientOwner)
	require.Equal(t, []string{owner.Email}, emails, "owner role must not include managers")
}

func TestRecipientEmails_OwnerAndManagers(t *testing.T) {
	_, db := testutil.App(t)

	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	inactiveManager := testutil.CreateUser(t, db, models.RoleSalesManager)
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", inactiveManager.ID).Update("is_active", false).Error)

	emails := recipientEmails(db, &owner.ID, models.NotificationRecipientOwnerAndManagers)
	require.Contains(t, emails, owner.Email)
	require.Contains(t, emails, manager.Email)
	require.NotContains(t, emails, inactiveManager.Email, "a deactivated manager must not be broadcast to")
	require.Len(t, emails, 2)
}

func TestRecipientEmails_NilOwnerStillIncludesManagers(t *testing.T) {
	_, db := testutil.App(t)

	manager := testutil.CreateUser(t, db, models.RoleSalesManager)

	emails := recipientEmails(db, nil, models.NotificationRecipientOwnerAndManagers)
	require.Equal(t, []string{manager.Email}, emails)
}

func TestRecipientEmails_DedupsOwnerWhoIsAlsoAManager(t *testing.T) {
	_, db := testutil.App(t)

	ownerManager := testutil.CreateUser(t, db, models.RoleSalesManager)

	emails := recipientEmails(db, &ownerManager.ID, models.NotificationRecipientOwnerAndManagers)
	require.Equal(t, []string{ownerManager.Email}, emails, "the same address must not be listed twice")
}

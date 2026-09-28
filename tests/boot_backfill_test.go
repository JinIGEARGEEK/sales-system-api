package apitests

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// rowVersion is Postgres' xmin for a row: it changes whenever the row is
// rewritten, even by an UPDATE that sets every column to its current value
// — so an unchanged xmin proves a boot backfill didn't touch the row.
func rowVersion(t *testing.T, db *gorm.DB, table string, id uint) string {
	t.Helper()
	var v string
	require.NoError(t, db.Raw("SELECT xmin::text FROM "+table+" WHERE id = ?", id).Scan(&v).Error)
	return v
}

func sortedTags(t *testing.T, db *gorm.DB, table string, id uint) []string {
	t.Helper()
	var tags pq.StringArray
	require.NoError(t, db.Raw("SELECT tags FROM "+table+" WHERE id = ?", id).Row().Scan(&tags))
	out := []string(tags)
	sort.Strings(out)
	return out
}

// BackfillLowercaseTags normalizes legacy rows but, unlike before, leaves
// already-normalized ones alone (it rewrote every tagged row on every
// boot), and a re-run writes nothing at all.
func TestBackfillLowercaseTags_OnlyRewritesRowsThatNeedIt(t *testing.T) {
	_, db := testutil.App(t)

	legacy := &models.Company{Name: "Legacy Co", Status: models.StatusActive}
	clean := &models.Company{Name: "Clean Co", Status: models.StatusActive}
	for _, c := range []*models.Company{legacy, clean} {
		require.NoError(t, db.Create(c).Error)
	}
	contact := seedContact(t, db, clean.ID)
	// Raw writes: Create/Update would normalize these themselves.
	require.NoError(t, db.Exec("UPDATE companies SET tags = ? WHERE id = ?", pq.StringArray{" VIP", "vip", "Enterprise", "  "}, legacy.ID).Error)
	require.NoError(t, db.Exec("UPDATE companies SET tags = ? WHERE id = ?", pq.StringArray{"vip", "enterprise"}, clean.ID).Error)
	require.NoError(t, db.Exec("UPDATE contacts SET tags = ? WHERE id = ?", pq.StringArray{"Decision Maker"}, contact.ID).Error)
	cleanBefore := rowVersion(t, db, "companies", clean.ID)

	require.NoError(t, database.BackfillLowercaseTags(db))

	assert.Equal(t, []string{"enterprise", "vip"}, sortedTags(t, db, "companies", legacy.ID))
	assert.Equal(t, []string{"decision maker"}, sortedTags(t, db, "contacts", contact.ID))
	assert.Equal(t, cleanBefore, rowVersion(t, db, "companies", clean.ID), "an already-normalized row isn't rewritten")

	versions := map[string]string{
		"legacy":  rowVersion(t, db, "companies", legacy.ID),
		"clean":   rowVersion(t, db, "companies", clean.ID),
		"contact": rowVersion(t, db, "contacts", contact.ID),
	}
	require.NoError(t, database.BackfillLowercaseTags(db))
	assert.Equal(t, versions["legacy"], rowVersion(t, db, "companies", legacy.ID), "re-run writes nothing")
	assert.Equal(t, versions["clean"], rowVersion(t, db, "companies", clean.ID))
	assert.Equal(t, versions["contact"], rowVersion(t, db, "contacts", contact.ID))
	assert.Equal(t, []string{"enterprise", "vip"}, sortedTags(t, db, "companies", legacy.ID))
}

// Two replicas booting at once run their migrations one after the other,
// never interleaved.
func TestWithBootLock_Serializes(t *testing.T) {
	_, db := testutil.App(t)
	ctx := context.Background()

	var mu sync.Mutex
	var events []string
	record := func(e string) { mu.Lock(); events = append(events, e); mu.Unlock() }

	firstHolds := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		assert.NoError(t, database.WithBootLock(ctx, db, func() error {
			record("first start")
			close(firstHolds)
			time.Sleep(200 * time.Millisecond)
			record("first end")
			return nil
		}))
	}()
	go func() {
		defer wg.Done()
		<-firstHolds
		assert.NoError(t, database.WithBootLock(ctx, db, func() error {
			record("second start")
			return nil
		}))
	}()
	wg.Wait()
	assert.Equal(t, []string{"first start", "first end", "second start"}, events)
}

// previous_stage is filled from the latest stage_changed audit row's
// "before" stage, only on Deals still NULL; a re-run changes nothing.
func TestBackfillStageEnteredAt_PreviousStageIdempotent(t *testing.T) {
	_, db := testutil.App(t)

	moved := seedDeal(t, db, nil)
	alreadySet := seedDeal(t, db, nil)
	neverMoved := seedDeal(t, db, nil)
	require.NoError(t, db.Model(moved).UpdateColumn("previous_stage", nil).Error)
	require.NoError(t, db.Model(alreadySet).UpdateColumn("previous_stage", "Negotiation").Error)
	require.NoError(t, db.Model(neverMoved).UpdateColumn("previous_stage", nil).Error)

	older := time.Now().Add(-48 * time.Hour)
	audits := []models.AuditLogEntry{
		{EntityType: "deal", EntityID: moved.ID, Action: "stage_changed", Before: models.JSONMap{"stage": "Lead"}, After: models.JSONMap{"stage": "Qualified"}, CreatedAt: older},
		{EntityType: "deal", EntityID: moved.ID, Action: "stage_changed", Before: models.JSONMap{"stage": "Qualified"}, After: models.JSONMap{"stage": "Proposal Sent"}},
		{EntityType: "deal", EntityID: moved.ID, Action: "updated", Before: models.JSONMap{"stage": "Ignored"}},
		{EntityType: "deal", EntityID: alreadySet.ID, Action: "stage_changed", Before: models.JSONMap{"stage": "Lead"}},
	}
	require.NoError(t, db.Create(&audits).Error)

	previous := func(id uint) *string {
		var d models.Deal
		require.NoError(t, db.First(&d, id).Error)
		return d.PreviousStage
	}

	require.NoError(t, database.BackfillStageEnteredAt(db))
	require.NotNil(t, previous(moved.ID))
	assert.Equal(t, "Qualified", *previous(moved.ID), "latest stage_changed row's before stage")
	assert.Equal(t, "Negotiation", *previous(alreadySet.ID), "an existing value is kept")
	assert.Nil(t, previous(neverMoved.ID), "no stage audit, nothing to fill")

	versions := []string{rowVersion(t, db, "deals", moved.ID), rowVersion(t, db, "deals", alreadySet.ID), rowVersion(t, db, "deals", neverMoved.ID)}
	require.NoError(t, database.BackfillStageEnteredAt(db))
	assert.Equal(t, versions, []string{rowVersion(t, db, "deals", moved.ID), rowVersion(t, db, "deals", alreadySet.ID), rowVersion(t, db, "deals", neverMoved.ID)}, "re-run writes nothing")
}

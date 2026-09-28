package notifier

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/calendar"
	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// sentMail is one captured sendMail call.
type sentMail struct{ to, subject string }

// captureMail swaps sendMail for a recorder for the rest of the test, so
// sends are observable without an SMTP server. fail makes every send
// return an error. Safe across goroutines (the concurrency test sends from
// two at once).
func captureMail(t *testing.T, fail bool) func() []sentMail {
	t.Helper()
	var mu sync.Mutex
	var sent []sentMail
	orig := sendMail
	sendMail = func(_ *config.Config, to, subject, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentMail{to, subject})
		if fail {
			return errors.New("smtp down")
		}
		return nil
	}
	t.Cleanup(func() { sendMail = orig })
	return func() []sentMail {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentMail(nil), sent...)
	}
}

func seedDueTask(t *testing.T, db *gorm.DB, title string, assignee *uint) *models.Task {
	t.Helper()
	task := &models.Task{Title: title, DueDate: time.Now().Add(-time.Hour), Status: models.TaskStatusPending, Priority: models.TaskPriorityMedium, AssignedTo: assignee}
	require.NoError(t, db.Create(task).Error)
	return task
}

func notifiedAt(t *testing.T, db *gorm.DB, id uint) *time.Time {
	t.Helper()
	var task models.Task
	require.NoError(t, db.First(&task, id).Error)
	return task.NotifiedAt
}

// Two instances' passes racing over the same due tasks send each reminder
// exactly once — the conditional claim lets only one of them through.
func TestCheckDueTasks_ConcurrentPassesSendOnce(t *testing.T) {
	_, db := testutil.App(t)
	sent := captureMail(t, false)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	const n = 5
	for i := 0; i < n; i++ {
		seedDueTask(t, db, "Call back", &owner.ID)
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			checkDueTasks(db, testutil.Config())
		}()
	}
	wg.Wait()
	// And a later pass finds nothing left to send.
	checkDueTasks(db, testutil.Config())

	assert.Len(t, sent(), n, "one email per task across both passes")
	for _, m := range sent() {
		assert.Equal(t, owner.Email, m.to)
	}
}

// The deterministic version of the race above: a second instance acting on
// its own (now stale) select of the same task loses the claim and sends
// nothing — send-then-stamp would have emailed twice.
func TestNotifyTaskDue_StaleSelectLosesClaim(t *testing.T) {
	_, db := testutil.App(t)
	sent := captureMail(t, false)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	task := seedDueTask(t, db, "Call back", &owner.ID)
	assignees := map[uint]models.User{owner.ID: *owner}

	require.NoError(t, notifyTaskDue(db, testutil.Config(), *task, assignees, nil))
	require.NoError(t, notifyTaskDue(db, testutil.Config(), *task, assignees, nil))

	assert.Len(t, sent(), 1)
}

// A task whose assignee was deleted or deactivated is stamped handled
// without an email — it used to fail "assignee not found" and be retried
// (and logged) every 15 minutes forever.
func TestCheckDueTasks_GoneAssigneeStampedNotSent(t *testing.T) {
	_, db := testutil.App(t)
	sent := captureMail(t, false)
	deleted := testutil.CreateUser(t, db, models.RoleSalesRep)
	inactive := testutil.CreateUser(t, db, models.RoleSalesRep)
	active := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Delete(deleted).Error)
	require.NoError(t, db.Model(inactive).UpdateColumn("is_active", false).Error)

	forDeleted := seedDueTask(t, db, "For deleted", &deleted.ID)
	forInactive := seedDueTask(t, db, "For inactive", &inactive.ID)
	forActive := seedDueTask(t, db, "For active", &active.ID)
	unassigned := seedDueTask(t, db, "Unassigned", nil)

	checkDueTasks(db, testutil.Config())

	for _, task := range []*models.Task{forDeleted, forInactive, forActive, unassigned} {
		assert.NotNil(t, notifiedAt(t, db, task.ID), "%s is stamped", task.Title)
	}
	require.Len(t, sent(), 1)
	assert.Equal(t, active.Email, sent()[0].to)
}

// A send that fails after the claim is logged, not retried: at-most-once.
func TestCheckDueTasks_SendFailureIsNotRetried(t *testing.T) {
	_, db := testutil.App(t)
	sent := captureMail(t, true)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	task := seedDueTask(t, db, "Call back", &owner.ID)

	checkDueTasks(db, testutil.Config())
	checkDueTasks(db, testutil.Config())

	assert.Len(t, sent(), 1)
	assert.NotNil(t, notifiedAt(t, db, task.ID))
}

// A task completed after the pass selected it isn't claimed or reminded.
func TestClaimTask_SkipsTaskNoLongerPending(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	task := seedDueTask(t, db, "Call back", &owner.ID)
	require.NoError(t, db.Model(task).UpdateColumn("status", models.TaskStatusDone).Error)

	claimed, err := claimTask(db, *task)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Nil(t, notifiedAt(t, db, task.ID))
}

// FR-CRM-101: a Sent Quote expiring within the threshold fires once (Task
// for the Deal's owner); one outside the window, one already past, and a
// Draft don't.
func TestQuoteExpiringRule(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	now := time.Now()
	date := func(days int) *string { s := now.AddDate(0, 0, days).Format("2006-01-02"); return &s }
	number := "QT2026090001"

	expiring := &models.Quote{DealID: deal.ID, Status: models.QuoteStatusSent, ValidityDate: date(3), Number: &number}
	require.NoError(t, db.Create(expiring).Error)
	for _, q := range []*models.Quote{
		{DealID: deal.ID, Status: models.QuoteStatusSent, ValidityDate: date(30)}, // outside the window
		{DealID: deal.ID, Status: models.QuoteStatusSent, ValidityDate: date(-2)}, // already expired
		{DealID: deal.ID, Status: models.QuoteStatusDraft, ValidityDate: date(3)}, // never sent
		{DealID: deal.ID, Status: models.QuoteStatusSent, ValidityDate: nil},      // no date
	} {
		require.NoError(t, db.Create(q).Error)
	}
	rule := seedRule(t, db, models.NotificationEntityQuote, 7, true)

	checkQuoteExpiringRule(db, testutil.Config(), rule, now)
	checkQuoteExpiringRule(db, testutil.Config(), rule, now)

	tasks := tasksFor(t, db)
	require.Len(t, tasks, 1, "only the expiring Sent quote fires, and only once")
	assert.Equal(t, "Quote "+number+" expires "+*date(3)+": Notifier Test Deal", tasks[0].Title)
	assert.Equal(t, models.RelatedTypeDeal, tasks[0].RelatedType)
	assert.Equal(t, deal.ID, tasks[0].RelatedID)
	require.NotNil(t, tasks[0].AssignedTo)
	assert.Equal(t, owner.ID, *tasks[0].AssignedTo)
	assert.True(t, alreadyNotified(db, rule.ID, expiring.ID, ""))
}

// FR-CRM-101: a Draft/Sent Contract unsigned past the threshold fires once;
// a newer one and a Signed one don't.
func TestContractStuckRule(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	now := time.Now()

	stuck := &models.Contract{DealID: deal.ID, Status: models.ContractStatusSent}
	fresh := &models.Contract{DealID: deal.ID, Status: models.ContractStatusDraft}
	signed := &models.Contract{DealID: deal.ID, Status: models.ContractStatusSigned}
	for _, c := range []*models.Contract{stuck, fresh, signed} {
		require.NoError(t, db.Create(c).Error)
	}
	require.NoError(t, db.Model(stuck).UpdateColumn("created_at", now.AddDate(0, 0, -20)).Error)
	require.NoError(t, db.Model(signed).UpdateColumn("created_at", now.AddDate(0, 0, -20)).Error)
	require.NoError(t, db.Model(fresh).UpdateColumn("created_at", now.AddDate(0, 0, -3)).Error)
	rule := seedRule(t, db, models.NotificationEntityContract, 14, true)

	checkContractStuckRule(db, testutil.Config(), rule, now)
	checkContractStuckRule(db, testutil.Config(), rule, now)

	tasks := tasksFor(t, db)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Contract unsigned 20 days: Notifier Test Deal", tasks[0].Title)
	require.NotNil(t, tasks[0].AssignedTo)
	assert.Equal(t, owner.ID, *tasks[0].AssignedTo)
	assert.True(t, alreadyNotified(db, rule.ID, stuck.ID, ""))
	assert.False(t, alreadyNotified(db, rule.ID, fresh.ID, ""))
}

// failingDealQueries returns a *gorm.DB on the same connection pool whose
// queries against the deals table fail — a separate gorm instance, so the
// injected callback can't leak into the shared test DB.
func failingDealQueries(t *testing.T, db *gorm.DB) *gorm.DB {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	broken, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	fail := func(tx *gorm.DB) {
		if tx.Statement.Table == "deals" {
			_ = tx.AddError(errors.New("injected deals failure"))
		}
	}
	require.NoError(t, broken.Callback().Query().Before("gorm:query").Register("test:fail_deals", fail))
	require.NoError(t, broken.Callback().Row().Before("gorm:row").Register("test:fail_deals_row", fail))
	return broken
}

// A failed query skips the day's snapshot instead of writing zeros (which
// the snapshot_date unique index then kept for the rest of the day); the
// next pass writes the real one, dated by the local calendar day.
func TestTakeForecastSnapshot_SkipsOnQueryErrorAndUsesLocalDay(t *testing.T) {
	_, db := testutil.App(t)
	now := time.Now()
	today := calendar.Today(now)
	closeDate := today.Format("2006-01-02")
	prob := 50
	commit := models.ForecastCategoryCommit
	deal := seedDealForNotifier(t, db, nil)
	require.NoError(t, db.Model(deal).Updates(map[string]interface{}{
		"status": models.DealStatusOpen, "expected_close_date": closeDate,
		"probability": prob, "forecast_category": commit,
	}).Error)

	takeForecastSnapshot(failingDealQueries(t, db), now)
	var count int64
	require.NoError(t, db.Model(&models.ForecastSnapshot{}).Count(&count).Error)
	assert.Zero(t, count, "no zero-valued row on a failed query")

	takeForecastSnapshot(db, now)
	takeForecastSnapshot(db, now)
	var snaps []models.ForecastSnapshot
	require.NoError(t, db.Find(&snaps).Error)
	require.Len(t, snaps, 1, "one row per day")
	snap := snaps[0]
	assert.Equal(t, today.Format("2006-01-02"), snap.SnapshotDate.Format("2006-01-02"), "local calendar day, not UTC's")
	assert.InDelta(t, 5000, snap.CommitValue, 0.001, "10000 × 50%")
	assert.Equal(t, today.Year(), snap.Year)
	assert.Equal(t, (int(today.Month())-1)/3+1, snap.Quarter)
}

// Just after local midnight, "today" is the local date. The old
// Truncate(24h) returned the previous (UTC) day there whenever the server's
// zone is ahead of UTC (Asia/Bangkok: until 07:00). Uses the process's own
// time.Local (swapping it mid-test races the driver's goroutines), so this
// only discriminates on a machine east of UTC — as production is.
func TestTakeForecastSnapshot_DateAtLocalMidnight(t *testing.T) {
	_, db := testutil.App(t)
	now := time.Date(2026, 9, 28, 0, 30, 0, 0, time.Local)
	takeForecastSnapshot(db, now)

	var snap models.ForecastSnapshot
	require.NoError(t, db.First(&snap).Error)
	assert.Equal(t, "2026-09-28", snap.SnapshotDate.Format("2006-01-02"))
}

// A panicking pass is logged and the job carries on (it used to take the
// whole process down); cancelling ctx stops the loop.
func TestRunEvery_RecoversPanicsAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	passes := 0
	runEvery(ctx, "test job", 5*time.Millisecond, func() {
		mu.Lock()
		passes++
		mu.Unlock()
		panic("boom")
	})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return passes >= 3
	}, 2*time.Second, 5*time.Millisecond, "keeps ticking after panics")

	cancel()
	require.True(t, Wait(2*time.Second), "job goroutine exits on cancel")
	mu.Lock()
	stopped := passes
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, stopped, passes, "no passes after cancel")
}

// A Sent quote is still valid through its whole validity date (local time),
// so on that last day it's inside the expiring window, even after 07:00
// Bangkok (UTC midnight of a bare date).
func TestQuoteExpiringRule_LastValidDayStillFires(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	lastDay := "2026-09-28"
	require.NoError(t, db.Create(&models.Quote{DealID: deal.ID, Status: models.QuoteStatusSent, ValidityDate: &lastDay}).Error)
	rule := seedRule(t, db, models.NotificationEntityQuote, 7, true)

	checkQuoteExpiringRule(db, testutil.Config(), rule, now)

	assert.Len(t, tasksFor(t, db), 1, "a quote on its last valid day is expiring, not expired")
}

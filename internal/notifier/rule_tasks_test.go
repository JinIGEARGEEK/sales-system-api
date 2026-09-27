package notifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// testutil.Config() has no SMTP_HOST, so every test here also exercises the
// "email off, alerts in-app" path the company actually runs in.

func seedRule(t *testing.T, db *gorm.DB, entity models.NotificationEntityType, thresholdDays int, createTask bool) models.NotificationRule {
	t.Helper()
	rule := models.NotificationRule{
		Name:          string(entity) + " rule " + time.Now().Format("150405.000000000"),
		EntityType:    entity,
		ThresholdDays: thresholdDays,
		RecipientRole: models.NotificationRecipientOwnerAndManagers,
		IsActive:      true,
		CreateTask:    true,
	}
	require.NoError(t, db.Create(&rule).Error)
	if !createTask {
		require.NoError(t, db.Model(&rule).UpdateColumn("create_task", false).Error)
		rule.CreateTask = false
	}
	return rule
}

func tasksFor(t *testing.T, db *gorm.DB) []models.Task {
	t.Helper()
	var tasks []models.Task
	require.NoError(t, db.Order("id").Find(&tasks).Error)
	return tasks
}

// A firing creates exactly one Task for the owner (not the managers), due
// today, pre-stamped NotifiedAt; the next tick doesn't duplicate it.
func TestDealIdleRule_CreatesOneTaskForOwner(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	testutil.CreateUser(t, db, models.RoleSalesManager)
	deal := seedDealForNotifier(t, db, &owner.ID)
	require.NoError(t, db.Model(deal).UpdateColumns(map[string]interface{}{
		"status": models.DealStatusOpen, "created_at": time.Now().AddDate(0, 0, -20),
	}).Error)
	rule := seedRule(t, db, models.NotificationEntityDeal, 14, true)

	checkDealIdleRule(db, testutil.Config(), rule)
	checkDealIdleRule(db, testutil.Config(), rule)

	tasks := tasksFor(t, db)
	require.Len(t, tasks, 1, "second tick must not duplicate the task")
	task := tasks[0]
	require.NotNil(t, task.AssignedTo)
	assert.Equal(t, owner.ID, *task.AssignedTo, "owner_and_managers still assigns only the owner")
	assert.Equal(t, "Deal idle 20 days: Notifier Test Deal", task.Title)
	assert.Equal(t, models.RelatedTypeDeal, task.RelatedType)
	assert.Equal(t, deal.ID, task.RelatedID)
	assert.Equal(t, models.TaskPriorityMedium, task.Priority)
	assert.Equal(t, models.TaskStatusPending, task.Status)
	assert.NotNil(t, task.NotifiedAt, "pre-stamped so the task-due reminder doesn't email again")
	today := time.Now()
	due := task.DueDate.In(time.Local)
	assert.Equal(t, []int{today.Year(), int(today.Month()), today.Day()}, []int{due.Year(), int(due.Month()), due.Day()})

	var logs int64
	db.Model(&models.NotificationLog{}).Where("rule_id = ?", rule.ID).Count(&logs)
	assert.EqualValues(t, 1, logs)
}

// A deactivated owner gets no Task (it used to be assigned to them, since
// the owner check didn't look at is_active) and, with nobody else to alert,
// nothing is logged — so reassigning to an active rep still alerts them.
func TestDealIdleRule_InactiveOwnerGetsNoTask(t *testing.T) {
	_, db := testutil.App(t)
	gone := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", gone.ID).Update("is_active", false).Error)
	deal := seedDealForNotifier(t, db, &gone.ID)
	require.NoError(t, db.Model(deal).UpdateColumns(map[string]interface{}{
		"status": models.DealStatusOpen, "created_at": time.Now().AddDate(0, 0, -20),
	}).Error)
	rule := seedRule(t, db, models.NotificationEntityDeal, 14, true)

	checkDealIdleRule(db, testutil.Config(), rule)
	assert.Empty(t, tasksFor(t, db), "no Task for a deactivated rep")
	assert.False(t, alreadyNotified(db, rule.ID, deal.ID, string(deal.Stage)), "nothing logged: nobody was alerted")

	successor := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(deal).UpdateColumn("assigned_to", successor.ID).Error)
	checkDealIdleRule(db, testutil.Config(), rule)
	tasks := tasksFor(t, db)
	require.Len(t, tasks, 1)
	assert.Equal(t, successor.ID, *tasks[0].AssignedTo)
}

// The dedupe key is the NotificationLog insert: if another tick (or
// instance) already wrote it, fireRule creates no Task.
func TestFireRule_NoTaskWhenLogAlreadyWritten(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	rule := seedRule(t, db, models.NotificationEntityDeal, 14, true)
	require.NoError(t, db.Create(&models.NotificationLog{RuleID: rule.ID, EntityID: 42, Context: "Lead", NotifiedAt: time.Now()}).Error)

	fireRule(db, testutil.Config(), rule, ruleFiring{
		EntityID: 42, Context: "Lead", OwnerID: &owner.ID, TaskTitle: "x",
		RelatedType: models.RelatedTypeDeal, RelatedID: 42,
	}, time.Now())

	assert.Empty(t, tasksFor(t, db))
}

func TestFireRule_CreateTaskFalseStillLogs(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	rule := seedRule(t, db, models.NotificationEntityDeal, 14, false)

	fireRule(db, testutil.Config(), rule, ruleFiring{
		EntityID: 7, OwnerID: &owner.ID, TaskTitle: "x", RelatedType: models.RelatedTypeDeal, RelatedID: 7,
	}, time.Now())

	assert.Empty(t, tasksFor(t, db), "create_task=false makes no Task")
	assert.True(t, alreadyNotified(db, rule.ID, 7, ""), "the email/log path still fires")
}

// With no owner and no recipient at all, nothing is recorded, so the entity
// can still fire once it gets an owner.
func TestFireRule_NobodyToAlertRecordsNothing(t *testing.T) {
	_, db := testutil.App(t)
	rule := seedRule(t, db, models.NotificationEntityDeal, 14, true)
	require.NoError(t, db.Model(&rule).UpdateColumn("recipient_role", models.NotificationRecipientOwner).Error)
	rule.RecipientRole = models.NotificationRecipientOwner

	fireRule(db, testutil.Config(), rule, ruleFiring{EntityID: 9, TaskTitle: "x"}, time.Now())
	assert.False(t, alreadyNotified(db, rule.ID, 9, ""))
}

// Overdue installments get a high-priority "Overdue payment" Task numbered
// by due-date position; one merely coming due gets a medium one.
func TestPaymentInstallmentRule_TaskTitleAndPriority(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	first := &models.PaymentInstallment{DealID: deal.ID, Amount: 5000, DueDate: time.Now().AddDate(0, 0, -40)}
	second := &models.PaymentInstallment{DealID: deal.ID, Amount: 5000, DueDate: time.Now().AddDate(0, 0, -3)}
	third := &models.PaymentInstallment{DealID: deal.ID, Amount: 5000, DueDate: time.Now().AddDate(0, 0, 4)}
	for _, inst := range []*models.PaymentInstallment{first, second, third} {
		require.NoError(t, db.Create(inst).Error)
	}
	// Installment #1 paid via an explicit link (cash + WHT).
	require.NoError(t, db.Create(&models.Payment{DealID: deal.ID, Amount: 4850, WhtAmount: 150, PaidAt: time.Now(),
		Method: models.PaymentMethodTransfer, InstallmentID: &first.ID}).Error)

	rule := seedRule(t, db, models.NotificationEntityPaymentInstallment, 7, true)
	checkPaymentInstallmentDueRule(db, testutil.Config(), rule, time.Now())

	tasks := tasksFor(t, db)
	byTitle := map[string]models.Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}
	require.Len(t, tasks, 2, "the paid installment must not fire")
	overdue, ok := byTitle["Overdue payment: Notifier Test Deal — installment 2"]
	require.True(t, ok, "titles: %v", byTitle)
	assert.Equal(t, models.TaskPriorityHigh, overdue.Priority)
	assert.Equal(t, models.RelatedTypeDeal, overdue.RelatedType)
	assert.Equal(t, deal.ID, overdue.RelatedID)
	upcoming, ok := byTitle["Payment due "+third.DueDate.Format("2006-01-02")+": Notifier Test Deal — installment 3"]
	require.True(t, ok, "titles: %v", byTitle)
	assert.Equal(t, models.TaskPriorityMedium, upcoming.Priority)
}

func TestInRenewalWindow(t *testing.T) {
	now := time.Date(2026, 9, 27, 22, 30, 0, 0, time.Local)
	day := func(offset int) time.Time { return time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset) }
	cases := []struct {
		offset int
		want   bool
	}{{31, false}, {30, true}, {0, true}, {-1, true}, {-30, true}, {-31, false}}
	for _, tc := range cases {
		days, ok := inRenewalWindow(day(tc.offset), now, 30)
		assert.Equal(t, tc.want, ok, "offset %d", tc.offset)
		assert.Equal(t, tc.offset, days)
	}
}

// Renewal fires once per renewal_date, and again for next cycle's date.
func TestCustomerProductRenewalRule_OncePerRenewalDate(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	product := &models.Product{Name: "CRM Cloud", Price: 12000, IsActive: true}
	require.NoError(t, db.Create(product).Error)

	now := time.Now()
	renewal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 10)
	cycle := models.BillingCycleYearly
	price := 12000.0
	cp := &models.CustomerProduct{CompanyID: deal.CompanyID, ProductID: product.ID, Status: models.CustomerProductActive,
		StartDate: now.AddDate(-1, 0, 0), RenewalDate: &renewal, BillingCycle: &cycle, Price: &price, SourceDealID: &deal.ID}
	require.NoError(t, db.Create(cp).Error)
	// Out-of-window and non-Active records never fire.
	far := renewal.AddDate(0, 0, 60)
	require.NoError(t, db.Create(&models.CustomerProduct{CompanyID: deal.CompanyID, ProductID: product.ID,
		Status: models.CustomerProductActive, StartDate: now, RenewalDate: &far}).Error)
	require.NoError(t, db.Create(&models.CustomerProduct{CompanyID: deal.CompanyID, ProductID: product.ID,
		Status: models.CustomerProductChurned, StartDate: now, RenewalDate: &renewal}).Error)

	rule := seedRule(t, db, models.NotificationEntityCustomerProductRenewal, 30, true)
	checkCustomerProductRenewalRule(db, testutil.Config(), rule, now)
	checkCustomerProductRenewalRule(db, testutil.Config(), rule, now)

	tasks := tasksFor(t, db)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Renewal due "+renewal.Format("2006-01-02")+": CRM Cloud — Notifier Test Co", tasks[0].Title)
	assert.Equal(t, models.RelatedTypeCompany, tasks[0].RelatedType)
	assert.Equal(t, deal.CompanyID, tasks[0].RelatedID)
	require.NotNil(t, tasks[0].AssignedTo)
	assert.Equal(t, owner.ID, *tasks[0].AssignedTo, "owner = source deal's assignee")
	assert.Contains(t, tasks[0].Description, "Billing cycle: yearly")
	assert.True(t, alreadyNotified(db, rule.ID, cp.ID, renewal.Format("2006-01-02")))

	// Renewed: next year's date is a new dedupe key and fires again once in window.
	next := renewal.AddDate(1, 0, 0)
	require.NoError(t, db.Model(cp).UpdateColumn("renewal_date", next).Error)
	checkCustomerProductRenewalRule(db, testutil.Config(), rule, next.AddDate(0, 0, -5))
	assert.Len(t, tasksFor(t, db), 2)
}

func TestContractExpiryRule_SignedOnlyOncePerEndDate(t *testing.T) {
	_, db := testutil.App(t)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 20)
	signed := &models.Contract{DealID: deal.ID, Status: models.ContractStatusSigned, EndDate: &end}
	require.NoError(t, db.Create(signed).Error)
	require.NoError(t, db.Create(&models.Contract{DealID: deal.ID, Status: models.ContractStatusDraft, EndDate: &end}).Error)

	rule := seedRule(t, db, models.NotificationEntityContractExpiry, 30, true)
	checkContractExpiryRule(db, testutil.Config(), rule, now)
	checkContractExpiryRule(db, testutil.Config(), rule, now)

	tasks := tasksFor(t, db)
	require.Len(t, tasks, 1, "draft contracts don't expire; no duplicate on the second tick")
	assert.Equal(t, "Contract ends "+end.Format("2006-01-02")+": Notifier Test Deal", tasks[0].Title)
	assert.Equal(t, models.RelatedTypeDeal, tasks[0].RelatedType)
	assert.True(t, alreadyNotified(db, rule.ID, signed.ID, end.Format("2006-01-02")))
}

// With SMTP off the whole ticker pass still runs its in-app work, and the
// due-task reminder marks tasks handled without erroring.
func TestNoSMTP_TickersStillDoInAppWork(t *testing.T) {
	_, db := testutil.App(t)
	cfg := testutil.Config()
	require.Empty(t, cfg.SMTPHost)
	owner := testutil.CreateUser(t, db, models.RoleSalesRep)
	deal := seedDealForNotifier(t, db, &owner.ID)
	require.NoError(t, db.Model(deal).UpdateColumns(map[string]interface{}{
		"status": models.DealStatusOpen, "created_at": time.Now().AddDate(0, 0, -30),
	}).Error)
	seedRule(t, db, models.NotificationEntityDeal, 14, true)
	due := &models.Task{Title: "Call back", DueDate: time.Now().Add(-time.Hour), Status: models.TaskStatusPending, AssignedTo: &owner.ID}
	require.NoError(t, db.Create(due).Error)

	checkWorkflowRules(db, cfg)
	checkDueTasks(db, cfg)

	var ruleTasks int64
	db.Model(&models.Task{}).Where("related_type = ? AND related_id = ?", models.RelatedTypeDeal, deal.ID).Count(&ruleTasks)
	assert.EqualValues(t, 1, ruleTasks, "rule tasks are created without SMTP")
	var reloaded models.Task
	require.NoError(t, db.First(&reloaded, due.ID).Error)
	assert.NotNil(t, reloaded.NotifiedAt, "due reminder is marked handled, not retried forever")
}

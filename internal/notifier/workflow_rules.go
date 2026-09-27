// workflow_rules.go — the background checker for Admin-configurable
// NotificationRule rows (FR-CRM-100/101/102). Runs on its own ticker (same
// interval as task_reminders.go's due-task checker, kept as a separate
// goroutine/ticker rather than folded into checkDueTasks so a bug in one
// checker can't stall the other).
package notifier

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const workflowRuleInterval = 15 * time.Minute

// StartWorkflowRuleReminders launches a background goroutine that
// periodically evaluates every active NotificationRule and, for any entity
// that newly matches, creates an in-app Task for its owner (rule.CreateTask)
// and emails the resolved recipients. The Task is the alert that always
// arrives: this company runs without SMTP, where utils.SendMail no-ops.
func StartWorkflowRuleReminders(db *gorm.DB, cfg *config.Config) {
	ticker := time.NewTicker(workflowRuleInterval)
	go func() {
		checkWorkflowRules(db, cfg)
		for range ticker.C {
			checkWorkflowRules(db, cfg)
		}
	}()
}

func checkWorkflowRules(db *gorm.DB, cfg *config.Config) {
	var rules []models.NotificationRule
	if err := db.Where("is_active = ?", true).Find(&rules).Error; err != nil {
		log.Printf("notifier: failed to query notification rules: %v", err)
		return
	}
	for _, rule := range rules {
		switch rule.EntityType {
		case models.NotificationEntityDeal:
			checkDealIdleRule(db, cfg, rule)
		case models.NotificationEntityQuote:
			checkQuoteExpiringRule(db, cfg, rule)
		case models.NotificationEntityContract:
			checkContractStuckRule(db, cfg, rule)
		case models.NotificationEntityProspect:
			checkProspectStaleRule(db, cfg, rule)
		case models.NotificationEntityCompany:
			checkCompanyDormantRule(db, cfg, rule)
		case models.NotificationEntityPaymentInstallment:
			checkPaymentInstallmentDueRule(db, cfg, rule, time.Now())
		case models.NotificationEntityCustomerProductRenewal:
			checkCustomerProductRenewalRule(db, cfg, rule, time.Now())
		case models.NotificationEntityContractExpiry:
			checkContractExpiryRule(db, cfg, rule, time.Now())
		}
	}
}

// alreadyNotified/recordNotified — the (rule_id, entity_id, context)
// idempotency check shared by every condition type. alreadyNotified is a
// cheap pre-check that skips building a firing; the authoritative dedupe is
// fireRule's conflict-safe insert. See
// models.NotificationLog's doc for why `context` varies by entity type
// (a Deal's current stage, so re-idling in a new stage can re-fire; empty
// for Quote/Contract, which only ever need to fire once per entity).
func alreadyNotified(db *gorm.DB, ruleID, entityID uint, context string) bool {
	var count int64
	db.Model(&models.NotificationLog{}).
		Where("rule_id = ? AND entity_id = ? AND context = ?", ruleID, entityID, context).
		Count(&count)
	return count > 0
}

func recordNotified(db *gorm.DB, ruleID, entityID uint, context string) error {
	return db.Create(&models.NotificationLog{RuleID: ruleID, EntityID: entityID, Context: context, NotifiedAt: time.Now()}).Error
}

// recipientEmails resolves who to email for a Deal-owned entity (Deal/Quote/
// Contract all ultimately hang off a Deal owner) per the rule's
// RecipientRole. There's no per-rep manager hierarchy in this schema, so
// "and managers" means every active Sales Manager, not one specific manager.
func recipientEmails(db *gorm.DB, ownerID *uint, role models.NotificationRecipientRole) []string {
	emails := []string{}
	seen := map[string]bool{}
	add := func(email string) {
		if email != "" && !seen[email] {
			seen[email] = true
			emails = append(emails, email)
		}
	}

	if ownerID != nil {
		var owner models.User
		if err := db.First(&owner, *ownerID).Error; err == nil {
			add(owner.Email)
		}
	}

	if role == models.NotificationRecipientOwnerAndManagers {
		var managers []models.User
		if err := db.Where("role = ? AND is_active = ?", models.RoleSalesManager, true).Find(&managers).Error; err == nil {
			for _, m := range managers {
				add(m.Email)
			}
		}
	}

	return emails
}

func sendRuleNotification(cfg *config.Config, emails []string, subject, body string) {
	for _, email := range emails {
		if err := utils.SendMail(cfg, email, subject, body); err != nil {
			log.Printf("notifier: failed to send workflow rule email to %s: %v", email, err)
		}
	}
}

// ruleFiring is one entity a rule matched on this tick: the idempotency key
// (EntityID, Context), who owns it, the email, and the in-app Task.
type ruleFiring struct {
	EntityID uint
	Context  string
	// OwnerID is who the Task is assigned to — the same person the email's
	// "owner" recipient resolves to (for owner_and_managers, only the owner
	// gets a Task; managers see it through the task list / notification log).
	OwnerID *uint

	Subject, Body string

	TaskTitle    string
	TaskPriority models.TaskPriority
	RelatedType  models.TaskRelatedType
	RelatedID    uint
}

// fireRule delivers one firing, at most once per (rule, entity, context).
//
// The NotificationLog row is the dedupe key (unique on rule_id, entity_id,
// context). It is inserted first with ON CONFLICT DO NOTHING, in the same
// transaction as the Task, so only the firing that actually writes the log
// row creates a Task — two overlapping ticks (or two API instances) can't
// both create one, and a failed Task insert rolls the log row back so the
// next tick retries. Emails go out after commit, best-effort as before.
//
// When there is nobody to alert at all (no recipient email and no Task
// assignee) nothing is recorded, so the firing happens later once the entity
// gets an owner — the same "skip, don't record" the email-only version did.
//
// The Task is due today (end of day, server-local) and is stamped
// NotifiedAt so task_reminders.go doesn't send a second "task due" email
// for an alert the rule email already covered.
func fireRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule, f ruleFiring, now time.Time) bool {
	emails := recipientEmails(db, f.OwnerID, rule.RecipientRole)

	var assignee *uint
	if rule.CreateTask && f.OwnerID != nil {
		var count int64
		db.Model(&models.User{}).Where("id = ?", *f.OwnerID).Count(&count)
		if count > 0 {
			assignee = f.OwnerID
		}
	}
	if len(emails) == 0 && assignee == nil {
		return false
	}

	fired := false
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.NotificationLog{RuleID: rule.ID, EntityID: f.EntityID, Context: f.Context, NotifiedAt: now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // already fired for this key
		}
		fired = true
		if assignee == nil {
			return nil
		}
		local := now.In(time.Local)
		task := models.Task{
			RelatedType: f.RelatedType, RelatedID: f.RelatedID,
			Title: f.TaskTitle, Description: f.Body,
			DueDate:    time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, time.Local),
			Status:     models.TaskStatusPending,
			Priority:   f.TaskPriority,
			AssignedTo: assignee,
			NotifiedAt: &now,
		}
		if task.Priority == "" {
			task.Priority = models.TaskPriorityMedium
		}
		return tx.Create(&task).Error
	})
	if err != nil {
		log.Printf("notifier: failed to record rule %d firing for entity %d: %v", rule.ID, f.EntityID, err)
		return false
	}
	if !fired {
		return false
	}
	sendRuleNotification(cfg, emails, f.Subject, f.Body)
	return true
}

// daysSince is whole elapsed days, for alert titles ("idle 14 days").
func daysSince(t, now time.Time) int {
	return int(now.Sub(t).Hours() / 24)
}

// checkDealIdleRule — FR-CRM-100. An open Deal (not yet Won/Lost) whose
// current stage has held for at least rule.ThresholdDays, measured from its
// most recent "stage_changed" audit entry (deals.go's UpdateStage — the only
// writer of that action) or Deal.CreatedAt if it never changed stage.
func checkDealIdleRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule) {
	var deals []models.Deal
	if err := db.Where("status = ?", models.DealStatusOpen).Find(&deals).Error; err != nil {
		log.Printf("notifier: failed to query deals for rule %d: %v", rule.ID, err)
		return
	}

	type lastTransition struct {
		EntityID  uint
		CreatedAt time.Time
	}
	var transitions []lastTransition
	if err := db.Table("audit_log_entries").
		Select("entity_id, MAX(created_at) as created_at").
		Where("entity_type = ? AND action = ?", "deal", "stage_changed").
		Group("entity_id").
		Scan(&transitions).Error; err != nil {
		log.Printf("notifier: failed to query stage transitions for rule %d: %v", rule.ID, err)
		return
	}
	lastChangeByDeal := make(map[uint]time.Time, len(transitions))
	for _, t := range transitions {
		lastChangeByDeal[t.EntityID] = t.CreatedAt
	}

	threshold := time.Duration(rule.ThresholdDays) * 24 * time.Hour
	now := time.Now()

	for _, deal := range deals {
		since := deal.CreatedAt
		if t, ok := lastChangeByDeal[deal.ID]; ok {
			since = t
		}
		if now.Sub(since) < threshold {
			continue
		}

		context := string(deal.Stage)
		if alreadyNotified(db, rule.ID, deal.ID, context) {
			continue
		}

		fireRule(db, cfg, rule, ruleFiring{
			EntityID: deal.ID, Context: context, OwnerID: deal.AssignedTo,
			Subject: fmt.Sprintf("Deal idle: %s", deal.Title),
			Body: fmt.Sprintf(
				"Reminder: the following deal has been in stage \"%s\" for %d+ days.\n\nDeal: %s\nStage: %s\n",
				deal.Stage, rule.ThresholdDays, deal.Title, deal.Stage,
			),
			TaskTitle:   fmt.Sprintf("Deal idle %d days: %s", daysSince(since, now), deal.Title),
			RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID,
		}, now)
	}
}

// checkQuoteExpiringRule — FR-CRM-101, same definition as the Quotes
// Expiring Soon report (FR-CRM-096): a Sent Quote whose validity_date falls
// within rule.ThresholdDays from now.
func checkQuoteExpiringRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule) {
	var quotes []models.Quote
	if err := db.Where("status = ?", models.QuoteStatusSent).Find(&quotes).Error; err != nil {
		log.Printf("notifier: failed to query quotes for rule %d: %v", rule.ID, err)
		return
	}

	now := time.Now()
	cutoff := now.Add(time.Duration(rule.ThresholdDays) * 24 * time.Hour)

	for _, quote := range quotes {
		validUntil, ok := models.ParseValidityDate(quote.ValidityDate)
		if !ok || validUntil.Before(now) || validUntil.After(cutoff) {
			continue
		}
		if alreadyNotified(db, rule.ID, quote.ID, "") {
			continue
		}

		var deal models.Deal
		if err := db.First(&deal, quote.DealID).Error; err != nil {
			continue
		}
		quoteLabel := "Quote"
		if quote.Number != nil {
			quoteLabel = "Quote " + *quote.Number
		}
		fireRule(db, cfg, rule, ruleFiring{
			EntityID: quote.ID, OwnerID: deal.AssignedTo,
			Subject: fmt.Sprintf("Quote expiring soon: %s", deal.Title),
			Body: fmt.Sprintf(
				"Reminder: a quote on the following deal expires within %d days.\n\nDeal: %s\nValidity date: %s\n",
				rule.ThresholdDays, deal.Title, validUntil.Format("2006-01-02"),
			),
			TaskTitle:   fmt.Sprintf("%s expires %s: %s", quoteLabel, validUntil.Format("2006-01-02"), deal.Title),
			RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID,
		}, now)
	}
}

// checkContractStuckRule — FR-CRM-101, same definition as the Contracts
// Stuck report (FR-CRM-097): a Draft/Sent Contract unsigned for at least
// rule.ThresholdDays since creation.
func checkContractStuckRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule) {
	var contracts []models.Contract
	if err := db.Where("status IN ?", []models.ContractStatus{models.ContractStatusDraft, models.ContractStatusSent}).
		Find(&contracts).Error; err != nil {
		log.Printf("notifier: failed to query contracts for rule %d: %v", rule.ID, err)
		return
	}

	threshold := time.Duration(rule.ThresholdDays) * 24 * time.Hour
	now := time.Now()

	for _, contract := range contracts {
		if now.Sub(contract.CreatedAt) < threshold {
			continue
		}
		if alreadyNotified(db, rule.ID, contract.ID, "") {
			continue
		}

		var deal models.Deal
		if err := db.First(&deal, contract.DealID).Error; err != nil {
			continue
		}
		fireRule(db, cfg, rule, ruleFiring{
			EntityID: contract.ID, OwnerID: deal.AssignedTo,
			Subject: fmt.Sprintf("Contract unsigned: %s", deal.Title),
			Body: fmt.Sprintf(
				"Reminder: a contract on the following deal has been unsigned for %d+ days.\n\nDeal: %s\nStatus: %s\n",
				rule.ThresholdDays, deal.Title, contract.Status,
			),
			TaskTitle:   fmt.Sprintf("Contract unsigned %d days: %s", daysSince(contract.CreatedAt, now), deal.Title),
			RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID,
		}, now)
	}
}

// Installment alert contexts: the rule fires once per installment per
// state, so the "due soon" reminder doesn't use up the dedupe key and
// swallow the later, higher-priority "overdue" one.
const (
	installmentContextDueSoon = "due_soon"
	installmentContextOverdue = "overdue"
)

// installmentAlertContext is the NotificationLog context for an unpaid
// installment's current status.
func installmentAlertContext(status string) string {
	if status == utils.InstallmentStatusOverdue {
		return installmentContextOverdue
	}
	return installmentContextDueSoon
}

// checkPaymentInstallmentDueRule — fires for each non-fully-paid
// PaymentInstallment whose due date falls within rule.ThresholdDays from now
// (covers both "coming due soon" and "already overdue" in one condition —
// see NotificationRule's own doc comment): at most once while it's due soon
// and once more when it becomes overdue (installmentAlertContext). Status is derived the same way
// the Payment Schedule UI and the Outstanding Balance report do
// (utils.ComputeInstallmentStatuses), grouped by Deal since the waterfall
// allocation needs each Deal's own running total-paid.
func checkPaymentInstallmentDueRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule, now time.Time) {
	var installments []models.PaymentInstallment
	if err := db.Find(&installments).Error; err != nil {
		log.Printf("notifier: failed to query payment installments for rule %d: %v", rule.ID, err)
		return
	}
	if len(installments) == 0 {
		return
	}

	byDeal := make(map[uint][]models.PaymentInstallment)
	dealIDs := make([]uint, 0, len(installments))
	for _, inst := range installments {
		if _, seen := byDeal[inst.DealID]; !seen {
			dealIDs = append(dealIDs, inst.DealID)
		}
		byDeal[inst.DealID] = append(byDeal[inst.DealID], inst)
	}

	// One grouped query for every Deal's total paid at once, instead of a
	// separate `SUM(amount) WHERE deal_id = ?` per Deal inside the loop
	// below — same batching reasoning as reports.go's
	// applyOutstandingBalanceAging, just for Payments instead of
	// Installments. This runs on a 15-minute ticker rather than a live
	// request, so the previous per-Deal query wasn't urgent, but there's no
	// reason to pay for it once it's this easy to avoid.
	//
	// Loads the Payment rows (not just a SUM) since installment_id links and
	// WHT both affect allocation (utils.ComputeInstallmentStatusesFromPayments).
	var payments []models.Payment
	if err := db.Where("deal_id IN ?", dealIDs).Find(&payments).Error; err != nil {
		log.Printf("notifier: failed to query payments for rule %d: %v", rule.ID, err)
		return
	}
	paymentsByDeal := make(map[uint][]models.Payment, len(dealIDs))
	for _, p := range payments {
		paymentsByDeal[p.DealID] = append(paymentsByDeal[p.DealID], p)
	}

	cutoff := now.Add(time.Duration(rule.ThresholdDays) * 24 * time.Hour)

	for dealID, dealInstallments := range byDeal {
		var deal *models.Deal
		// Statuses come back in due-date order, so position = installment number.
		for i, s := range utils.ComputeInstallmentStatusesFromPayments(dealInstallments, paymentsByDeal[dealID], now) {
			if s.Status == utils.InstallmentStatusPaid {
				continue
			}
			if s.Installment.DueDate.After(cutoff) {
				continue
			}
			context := installmentAlertContext(s.Status)
			if alreadyNotified(db, rule.ID, s.Installment.ID, context) {
				continue
			}

			if deal == nil {
				var d models.Deal
				if err := db.First(&d, dealID).Error; err != nil {
					break
				}
				deal = &d
			}
			title := fmt.Sprintf("Payment due %s: %s — installment %d", s.Installment.DueDate.Format("2006-01-02"), deal.Title, i+1)
			priority := models.TaskPriorityMedium
			if s.Status == utils.InstallmentStatusOverdue {
				title = fmt.Sprintf("Overdue payment: %s — installment %d", deal.Title, i+1)
				priority = models.TaskPriorityHigh
			}
			fireRule(db, cfg, rule, ruleFiring{
				EntityID: s.Installment.ID, Context: context, OwnerID: deal.AssignedTo,
				Subject: fmt.Sprintf("Payment installment due: %s", deal.Title),
				Body: fmt.Sprintf(
					"Reminder: a payment installment on the following deal is %s.\n\nDeal: %s\nAmount: %.2f\nDue date: %s\n",
					s.Status, deal.Title, s.Installment.Amount, s.Installment.DueDate.Format("2006-01-02"),
				),
				TaskTitle: title, TaskPriority: priority,
				RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID,
			}, now)
		}
	}
}

// checkProspectStaleRule — FR-CRM-107, added 2026-09-03. A Prospect still
// actively being worked (status not yet Converted/Disqualified) that has
// gone at least rule.ThresholdDays with no update. Uses UpdatedAt rather
// than a stage-transition audit lookup like checkDealIdleRule, since
// Prospect.Status changes aren't separately audited the way Deal.Stage is —
// UpdatedAt is the closest available "last touched" signal (any field edit
// bumps it, not just a status change).
//
// **Updated 2026-09-09**: the "disqualified" exclusion resolves the
// configured ProspectStage row's IsDisqualifiedStage flag instead of the
// hardcoded models.ProspectStatusDisqualified literal, since Prospect
// stages became Admin-configurable/renamable the same day (see
// ProspectStage's own doc) — an Admin renaming "Disqualified" would
// otherwise leave genuinely-disqualified Prospects incorrectly eligible for
// this rule. Falls back to the literal name if no row is flagged (e.g.
// right after a migration, before the seed runs), same fallback shape as
// utils.IsWonStage/IsLostStage use for Deal stages. "Converted" stays a
// literal check — it's deliberately never a ProspectStage row (see
// ProspectStatusConverted's own doc).
func checkProspectStaleRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule) {
	disqualifiedStageName := string(models.ProspectStatusDisqualified)
	var disqualifiedStage models.ProspectStage
	if err := db.Where("is_disqualified_stage = ?", true).First(&disqualifiedStage).Error; err == nil {
		disqualifiedStageName = disqualifiedStage.Name
	}

	var prospects []models.Prospect
	if err := db.Where("status NOT IN ?", []string{string(models.ProspectStatusConverted), disqualifiedStageName}).
		Find(&prospects).Error; err != nil {
		log.Printf("notifier: failed to query prospects for rule %d: %v", rule.ID, err)
		return
	}

	threshold := time.Duration(rule.ThresholdDays) * 24 * time.Hour
	now := time.Now()

	for _, prospect := range prospects {
		if now.Sub(prospect.UpdatedAt) < threshold {
			continue
		}

		// context = current status, same reasoning as checkDealIdleRule's
		// stage context — re-idling after a status change (e.g. New ->
		// Engaging, then stuck again) can re-fire instead of being
		// permanently suppressed by one earlier notification.
		context := string(prospect.Status)
		if alreadyNotified(db, rule.ID, prospect.ID, context) {
			continue
		}

		fireRule(db, cfg, rule, ruleFiring{
			EntityID: prospect.ID, Context: context, OwnerID: prospect.AssignedTo,
			Subject: fmt.Sprintf("Prospect stale: %s", prospect.Name),
			Body: fmt.Sprintf(
				"Reminder: the following prospect has had no updates in %d+ days.\n\nProspect: %s\nStatus: %s\n",
				rule.ThresholdDays, prospect.Name, prospect.Status,
			),
			TaskTitle:   fmt.Sprintf("Prospect stale %d days: %s", daysSince(prospect.UpdatedAt, now), prospect.Name),
			RelatedType: models.RelatedTypeProspect, RelatedID: prospect.ID,
		}, now)
	}
}

// companyDormantTiers are this rule's own fixed 60/90/120-day escalation
// boundaries. **Updated 2026-09-09**: these used to be kept deliberately in
// sync with the dashboard's upsell_opportunities widget, which had the same
// fixed tiers — but f876697 replaced that widget's tiers with an
// Admin/user-configurable upsell_min_stale_days threshold
// (internal/handlers/dashboard.go's upsellOpportunities), so the two are no
// longer related. This rule still escalates through its own fixed tiers
// rather than firing once past a single caller-configured threshold, since
// unlike checkDealIdleRule/checkQuoteExpiringRule/checkContractStuckRule it
// needs to re-fire as a Company gets progressively more stale;
// rule.ThresholdDays is still honored as the floor below which nothing fires
// at all (see the loop below).
var companyDormantTiers = []int{60, 90, 120}

// checkCompanyDormantRule — dormant-customer / upsell-targeting feature. An
// active Company with no Activity logged directly against it (related_type =
// "company", same company-scoped-only definition as
// internal/handlers/company_activity.go's withLastActivityAt — deliberately
// NOT rolled up from that Company's Deals/Contacts) for at least
// rule.ThresholdDays. The LEFT JOIN subquery below duplicates that handler's
// SQL rather than importing it, mirroring how checkDealIdleRule already
// builds its own raw audit_log_entries query instead of reaching into the
// handlers package — this notifier package stays self-contained.
//
// Idempotency context is the coarse stale tier ("60"/"90"/"120", whichever
// boundary the Company just crossed) rather than a fixed value, so escalating
// into a worse tier can re-fire even though an earlier, milder tier already
// logged a notification — same reasoning as checkDealIdleRule's stage-as-
// context. A Company with no Activity at all is always treated as the most
// stale tier (120).
func checkCompanyDormantRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule) {
	var rows []struct {
		ID             uint
		Name           string
		LastActivityAt *time.Time
	}
	if err := db.Table("companies").
		Select("companies.id, companies.name, last_company_activity.last_activity_at as last_activity_at").
		Joins("LEFT JOIN (SELECT related_id, MAX(created_at) as last_activity_at FROM activities WHERE related_type = ? GROUP BY related_id) as last_company_activity ON last_company_activity.related_id = companies.id", models.RelatedTypeCompany).
		Where("companies.status = ?", models.StatusActive).
		Scan(&rows).Error; err != nil {
		log.Printf("notifier: failed to query companies for rule %d: %v", rule.ID, err)
		return
	}

	now := time.Now()

	for _, row := range rows {
		var daysSince int
		if row.LastActivityAt == nil {
			daysSince = companyDormantTiers[len(companyDormantTiers)-1]
		} else {
			daysSince = int(now.Sub(*row.LastActivityAt).Hours() / 24)
		}
		if daysSince < rule.ThresholdDays {
			continue
		}

		// The tier just crossed — the largest boundary daysSince has reached,
		// falling back to the smallest if it hasn't reached any (still ≥
		// rule.ThresholdDays, which may be below companyDormantTiers[0]).
		tier := companyDormantTiers[0]
		for _, t := range companyDormantTiers {
			if daysSince >= t {
				tier = t
			}
		}
		context := fmt.Sprintf("%d", tier)
		if alreadyNotified(db, rule.ID, row.ID, context) {
			continue
		}

		// ownerID resolves to the company's most-recent Deal's AssignedTo, nil
		// if the Company has no Deals at all — recipientEmails' existing
		// nil-ownerID behavior (only adds an owner email when ownerID != nil)
		// already gives the desired "falls back to whatever RecipientRole
		// broadcasts to" behavior with no special-casing needed here.
		var ownerID *uint
		var mostRecentDeal models.Deal
		if err := db.Where("company_id = ?", row.ID).Order("created_at DESC").First(&mostRecentDeal).Error; err == nil {
			ownerID = mostRecentDeal.AssignedTo
		}

		fireRule(db, cfg, rule, ruleFiring{
			EntityID: row.ID, Context: context, OwnerID: ownerID,
			Subject: fmt.Sprintf("Company gone quiet: %s", row.Name),
			Body: fmt.Sprintf(
				"Reminder: the following company has had no logged activity in %d+ days — a possible upsell/renewal target worth reaching out to.\n\nCompany: %s\n",
				tier, row.Name,
			),
			TaskTitle:   fmt.Sprintf("Company quiet %d+ days: %s", tier, row.Name),
			RelatedType: models.RelatedTypeCompany, RelatedID: row.ID,
		}, now)
	}
}

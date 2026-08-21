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

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const workflowRuleInterval = 15 * time.Minute

// StartWorkflowRuleReminders launches a background goroutine that
// periodically evaluates every active NotificationRule and emails the
// resolved recipients for any entity that newly matches. Safe to call even
// when SMTP isn't configured — utils.SendMail no-ops in that case.
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
		}
	}
}

// alreadyNotified/recordNotified — the (rule_id, entity_id, context)
// idempotency check shared by all three condition types. See
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

		emails := recipientEmails(db, deal.AssignedTo, rule.RecipientRole)
		if len(emails) == 0 {
			continue
		}
		subject := fmt.Sprintf("Deal idle: %s", deal.Title)
		body := fmt.Sprintf(
			"Reminder: the following deal has been in stage \"%s\" for %d+ days.\n\nDeal: %s\nStage: %s\n",
			deal.Stage, rule.ThresholdDays, deal.Title, deal.Stage,
		)
		sendRuleNotification(cfg, emails, subject, body)
		if err := recordNotified(db, rule.ID, deal.ID, context); err != nil {
			log.Printf("notifier: failed to record notification for deal %d: %v", deal.ID, err)
		}
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
		emails := recipientEmails(db, deal.AssignedTo, rule.RecipientRole)
		if len(emails) == 0 {
			continue
		}
		subject := fmt.Sprintf("Quote expiring soon: %s", deal.Title)
		body := fmt.Sprintf(
			"Reminder: a quote on the following deal expires within %d days.\n\nDeal: %s\nValidity date: %s\n",
			rule.ThresholdDays, deal.Title, validUntil.Format("2006-01-02"),
		)
		sendRuleNotification(cfg, emails, subject, body)
		if err := recordNotified(db, rule.ID, quote.ID, ""); err != nil {
			log.Printf("notifier: failed to record notification for quote %d: %v", quote.ID, err)
		}
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
		emails := recipientEmails(db, deal.AssignedTo, rule.RecipientRole)
		if len(emails) == 0 {
			continue
		}
		subject := fmt.Sprintf("Contract unsigned: %s", deal.Title)
		body := fmt.Sprintf(
			"Reminder: a contract on the following deal has been unsigned for %d+ days.\n\nDeal: %s\nStatus: %s\n",
			rule.ThresholdDays, deal.Title, contract.Status,
		)
		sendRuleNotification(cfg, emails, subject, body)
		if err := recordNotified(db, rule.ID, contract.ID, ""); err != nil {
			log.Printf("notifier: failed to record notification for contract %d: %v", contract.ID, err)
		}
	}
}

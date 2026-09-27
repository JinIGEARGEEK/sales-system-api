// renewal_rules.go — the customer_product_renewal and contract_expiry
// NotificationRule types: date-driven reminders for packaged Products, which
// are charged via Contracts and renew on a cycle (custom Projects are quoted
// and don't renew). Evaluated by checkWorkflowRules alongside the others.
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

// inRenewalWindow reports whether a calendar date `date` is due for a
// renewal-style alert today: no more than thresholdDays ahead and no more
// than models.RenewalGraceDays behind. Returns the day offset too (negative
// = already past) for the alert wording.
func inRenewalWindow(date, now time.Time, thresholdDays int) (int, bool) {
	days := utils.DaysUntil(utils.Today(now), date)
	return days, days <= thresholdDays && days >= -models.RenewalGraceDays
}

// renewalContext is the NotificationLog context for a date-driven rule: the
// date itself, so each renewal/end date fires once, and next cycle's date
// (a different context) fires again.
func renewalContext(date time.Time) string {
	return utils.CalendarDay(date).Format("2006-01-02")
}

// whenLabel phrases a day offset for titles: "today", "in 5 days",
// "3 days ago".
func whenLabel(days int) string {
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days > 1:
		return fmt.Sprintf("in %d days", days)
	case days == -1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", -days)
	}
}

// checkCustomerProductRenewalRule — Active CustomerProducts whose
// renewal_date is inside the window (inRenewalWindow). The owner is the
// source Deal's assignee when source_deal_id is set, else the Company's most
// recent Deal's assignee (the same fallback checkCompanyDormantRule uses).
// The Task relates to the Company, the customer the product belongs to.
func checkCustomerProductRenewalRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule, now time.Time) {
	var records []models.CustomerProduct
	if err := db.Where("status = ? AND renewal_date IS NOT NULL", models.CustomerProductActive).
		Find(&records).Error; err != nil {
		log.Printf("notifier: failed to query customer products for rule %d: %v", rule.ID, err)
		return
	}

	for _, cp := range records {
		days, due := inRenewalWindow(*cp.RenewalDate, now, rule.ThresholdDays)
		if !due {
			continue
		}
		context := renewalContext(*cp.RenewalDate)
		if alreadyNotified(db, rule.ID, cp.ID, context) {
			continue
		}

		var company models.Company
		if err := db.First(&company, cp.CompanyID).Error; err != nil {
			continue // company deleted — nobody to renew with
		}
		var product models.Product
		productName := fmt.Sprintf("Product #%d", cp.ProductID)
		if err := db.Unscoped().First(&product, cp.ProductID).Error; err == nil {
			productName = product.Name
		}

		var ownerID *uint
		if cp.SourceDealID != nil {
			var deal models.Deal
			if err := db.First(&deal, *cp.SourceDealID).Error; err == nil {
				ownerID = deal.AssignedTo
			}
		}
		if ownerID == nil {
			var recent models.Deal
			if err := db.Where("company_id = ?", company.ID).Order("created_at DESC, id DESC").First(&recent).Error; err == nil {
				ownerID = recent.AssignedTo
			}
		}

		body := fmt.Sprintf("Reminder: a product subscription renews %s.\n\nCompany: %s\nProduct: %s\nRenewal date: %s\n",
			whenLabel(days), company.Name, productName, context)
		if cp.BillingCycle != nil {
			body += fmt.Sprintf("Billing cycle: %s\n", *cp.BillingCycle)
		}
		if cp.Price != nil {
			body += fmt.Sprintf("Price: %.2f\n", *cp.Price)
		}

		fireRule(db, cfg, rule, ruleFiring{
			EntityID: cp.ID, Context: context, OwnerID: ownerID,
			Subject:     fmt.Sprintf("Renewal due %s: %s — %s", context, productName, company.Name),
			Body:        body,
			TaskTitle:   fmt.Sprintf("Renewal due %s: %s — %s", context, productName, company.Name),
			RelatedType: models.RelatedTypeCompany, RelatedID: company.ID,
		}, now)
	}
}

// checkContractExpiryRule — Signed Contracts whose end_date is inside the
// window. Owned by the Deal's assignee; the Task relates to the Deal.
func checkContractExpiryRule(db *gorm.DB, cfg *config.Config, rule models.NotificationRule, now time.Time) {
	var contracts []models.Contract
	if err := db.Where("status = ? AND end_date IS NOT NULL", models.ContractStatusSigned).
		Find(&contracts).Error; err != nil {
		log.Printf("notifier: failed to query contracts for rule %d: %v", rule.ID, err)
		return
	}

	for _, contract := range contracts {
		days, due := inRenewalWindow(*contract.EndDate, now, rule.ThresholdDays)
		if !due {
			continue
		}
		context := renewalContext(*contract.EndDate)
		if alreadyNotified(db, rule.ID, contract.ID, context) {
			continue
		}
		var deal models.Deal
		if err := db.First(&deal, contract.DealID).Error; err != nil {
			continue
		}

		verb := "ends"
		if days < 0 {
			verb = "ended"
		}
		fireRule(db, cfg, rule, ruleFiring{
			EntityID: contract.ID, Context: context, OwnerID: deal.AssignedTo,
			Subject: fmt.Sprintf("Contract %s %s: %s", verb, context, deal.Title),
			Body: fmt.Sprintf("Reminder: a signed contract %s %s — time to talk renewal.\n\nDeal: %s\nEnd date: %s\n",
				verb, whenLabel(days), deal.Title, context),
			TaskTitle:   fmt.Sprintf("Contract %s %s: %s", verb, context, deal.Title),
			RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID,
		}, now)
	}
}

package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// NotificationLogHandler surfaces recent NotificationRule firings in-app
// (dashboard "Recent Alerts" widget) — previously email-only, so a rep with
// no/misconfigured SMTP had zero visibility that anything fired at all.
type NotificationLogHandler struct {
	DB *gorm.DB
}

func NewNotificationLogHandler(db *gorm.DB) *NotificationLogHandler {
	return &NotificationLogHandler{DB: db}
}

// notificationLogListLimit caps the response, not the initial DB fetch below
// (which over-fetches on purpose — see List's comment).
const notificationLogListLimit = 20

type notificationFiringRow struct {
	ID         uint      `json:"id"`
	RuleName   string    `json:"rule_name"`
	EntityType string    `json:"entity_type"`
	DealID     uint      `json:"deal_id"`
	DealTitle  string    `json:"deal_title"`
	NotifiedAt time.Time `json:"notified_at"`
}

// List — GET /notification-log. Any authenticated role — scoping happens
// per-row via CanWrite rather than a RequireRoles route gate: a Sales Rep
// only sees firings for Deals they own, Admin/Sales Manager see everything.
// This approximates workflow_rules.go's recipientEmails() owner/
// owner_and_managers resolution without needing a persisted per-firing
// recipient list (NotificationLog only records that a rule fired for an
// entity, not who was emailed).
func (h *NotificationLogHandler) List(c *fiber.Ctx) error {
	// Over-fetch (200) before per-row CanWrite scoping + the real 20-row
	// response cap below, so a Sales Rep whose relevant firings aren't among
	// the newest 20 company-wide still sees a full list of their own.
	var logs []models.NotificationLog
	if err := h.DB.Order("notified_at DESC").Limit(200).Find(&logs).Error; err != nil {
		return utils.Internal(c, "Failed to list notification log")
	}

	ruleIDs := make([]uint, 0, len(logs))
	for _, l := range logs {
		ruleIDs = append(ruleIDs, l.RuleID)
	}
	ruleByID := map[uint]models.NotificationRule{}
	if len(ruleIDs) > 0 {
		var rules []models.NotificationRule
		h.DB.Where("id IN ?", ruleIDs).Find(&rules)
		for _, r := range rules {
			ruleByID[r.ID] = r
		}
	}

	rows := []notificationFiringRow{}
	for _, l := range logs {
		if len(rows) >= notificationLogListLimit {
			break
		}
		rule, ok := ruleByID[l.RuleID]
		if !ok {
			continue // rule since deleted — DELETE on NotificationRule is soft (is_active), so this shouldn't happen, but don't crash on it
		}
		deal, ok := h.resolveDeal(rule.EntityType, l.EntityID)
		if !ok {
			continue // entity since deleted
		}
		if !CanWrite(c, deal.AssignedTo) {
			continue
		}
		rows = append(rows, notificationFiringRow{
			ID: l.ID, RuleName: rule.Name, EntityType: string(rule.EntityType),
			DealID: deal.ID, DealTitle: deal.Title, NotifiedAt: l.NotifiedAt,
		})
	}
	return utils.OK(c, rows)
}

// resolveDeal resolves a NotificationRule's firing (a Deal, Quote, or
// Contract id depending on EntityType) down to the Deal it belongs to —
// Quote/Contract firings both need one extra hop via DealID, same as
// checkQuoteExpiringRule/checkContractStuckRule in
// internal/notifier/workflow_rules.go.
func (h *NotificationLogHandler) resolveDeal(entityType models.NotificationEntityType, entityID uint) (models.Deal, bool) {
	var deal models.Deal
	switch entityType {
	case models.NotificationEntityDeal:
		if err := h.DB.First(&deal, entityID).Error; err != nil {
			return models.Deal{}, false
		}
	case models.NotificationEntityQuote:
		var quote models.Quote
		if err := h.DB.First(&quote, entityID).Error; err != nil {
			return models.Deal{}, false
		}
		if err := h.DB.First(&deal, quote.DealID).Error; err != nil {
			return models.Deal{}, false
		}
	case models.NotificationEntityContract:
		var contract models.Contract
		if err := h.DB.First(&contract, entityID).Error; err != nil {
			return models.Deal{}, false
		}
		if err := h.DB.First(&deal, contract.DealID).Error; err != nil {
			return models.Deal{}, false
		}
	default:
		return models.Deal{}, false
	}
	return deal, true
}

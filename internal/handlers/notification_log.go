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
	ID         uint   `json:"id"`
	RuleName   string `json:"rule_name"`
	EntityType string `json:"entity_type"`
	DealID     uint   `json:"deal_id,omitempty"`
	DealTitle  string `json:"deal_title,omitempty"`
	// ProspectID/ProspectName — added 2026-09-03 alongside the "prospect"
	// entity type (FR-CRM-107). Kept as separate, additive fields rather
	// than renaming DealID/DealTitle to something generic, so existing
	// Deal/Quote/Contract consumers (the dashboard's Recent Alerts widget)
	// are untouched.
	ProspectID   uint   `json:"prospect_id,omitempty"`
	ProspectName string `json:"prospect_name,omitempty"`
	// CompanyID/CompanyName — added alongside the "company" entity type
	// (dormant-customer / upsell-targeting feature), same additive pattern as
	// ProspectID/ProspectName above.
	CompanyID   uint      `json:"company_id,omitempty"`
	CompanyName string    `json:"company_name,omitempty"`
	NotifiedAt  time.Time `json:"notified_at"`
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

	resolved := h.resolveEntities(logs, ruleByID)

	rows := []notificationFiringRow{}
	for _, l := range logs {
		if len(rows) >= notificationLogListLimit {
			break
		}
		rule, ok := ruleByID[l.RuleID]
		if !ok {
			continue // rule since deleted — DELETE on NotificationRule is soft (is_active), so this shouldn't happen, but don't crash on it
		}

		switch rule.EntityType {
		case models.NotificationEntityProspect:
			prospect, ok := resolved.prospects[l.EntityID]
			if !ok || !CanWrite(c, prospect.AssignedTo) {
				continue
			}
			rows = append(rows, notificationFiringRow{
				ID: l.ID, RuleName: rule.Name, EntityType: string(rule.EntityType),
				ProspectID: prospect.ID, ProspectName: prospect.Name, NotifiedAt: l.NotifiedAt,
			})

		case models.NotificationEntityCompany:
			company, ok := resolved.companies[l.EntityID]
			if !ok {
				continue
			}
			// Same most-recent-Deal-owner resolution as
			// checkCompanyDormantRule (internal/notifier/workflow_rules.go) —
			// nil when the Company has no Deals at all (resolved.companyDealOwner
			// simply has no entry). Passed straight into CanWrite unchanged,
			// exactly like a Deal's own AssignedTo below (also nilable for an
			// unassigned Deal) — CanWrite's existing assignedTo == nil rule
			// already governs that case identically, no special-casing needed.
			ownerID := resolved.companyDealOwner[company.ID]
			if !CanWrite(c, ownerID) {
				continue
			}
			rows = append(rows, notificationFiringRow{
				ID: l.ID, RuleName: rule.Name, EntityType: string(rule.EntityType),
				CompanyID: company.ID, CompanyName: company.Name, NotifiedAt: l.NotifiedAt,
			})

		default:
			deal, ok := resolved.dealFor(rule.EntityType, l.EntityID)
			if !ok || !CanWrite(c, deal.AssignedTo) {
				continue
			}
			rows = append(rows, notificationFiringRow{
				ID: l.ID, RuleName: rule.Name, EntityType: string(rule.EntityType),
				DealID: deal.ID, DealTitle: deal.Title, NotifiedAt: l.NotifiedAt,
			})
		}
	}
	return utils.OK(c, rows)
}

// resolvedEntities holds every entity List's row-building loop needs,
// batch-loaded once up front instead of one query per log row (the previous
// approach could issue 200-400+ queries for a single request — up to two
// sequential lookups per row, times up to 200 over-fetched rows). ownerID for
// each Deal/Quote/Contract firing is resolved by id lookup in dealFor;
// Quote/Contract rows are pre-resolved down to their owning Deal ID during
// resolveEntities so dealFor is a single map read, no query.
type resolvedEntities struct {
	prospects         map[uint]models.Prospect
	companies         map[uint]models.Company
	companyDealOwner  map[uint]*uint // company id -> most recent Deal's AssignedTo
	deals             map[uint]models.Deal
	dealIDForQuote    map[uint]uint // quote id -> deal id
	dealIDForContract map[uint]uint // contract id -> deal id
}

// dealFor resolves a NotificationRule firing (a Deal, Quote, or Contract id
// depending on entityType) down to the Deal it belongs to, purely from the
// maps resolveEntities already populated — Quote/Contract firings both need
// one extra hop via their own DealID, same logical resolution
// checkQuoteExpiringRule/checkContractStuckRule (internal/notifier/
// workflow_rules.go) perform, just pre-batched instead of per-row.
func (r resolvedEntities) dealFor(entityType models.NotificationEntityType, entityID uint) (models.Deal, bool) {
	switch entityType {
	case models.NotificationEntityDeal:
		deal, ok := r.deals[entityID]
		return deal, ok
	case models.NotificationEntityQuote:
		dealID, ok := r.dealIDForQuote[entityID]
		if !ok {
			return models.Deal{}, false
		}
		deal, ok := r.deals[dealID]
		return deal, ok
	case models.NotificationEntityContract:
		dealID, ok := r.dealIDForContract[entityID]
		if !ok {
			return models.Deal{}, false
		}
		deal, ok := r.deals[dealID]
		return deal, ok
	default:
		return models.Deal{}, false
	}
}

// dealIDsFor batch-loads T by id (via one `IN (...)` query, or none at all
// if ids is empty) and returns id -> DealID via getIDAndDealID — the shared
// "resolve down to the owning Deal ID" shape behind Quote/Contract in
// resolveEntities below, which otherwise differ only by model type.
func dealIDsFor[T any](db *gorm.DB, ids []uint, getIDAndDealID func(T) (id, dealID uint)) map[uint]uint {
	out := map[uint]uint{}
	if len(ids) == 0 {
		return out
	}
	var rows []T
	db.Where("id IN ?", ids).Find(&rows)
	for _, row := range rows {
		id, dealID := getIDAndDealID(row)
		out[id] = dealID
	}
	return out
}

// resolveEntities batch-loads every Prospect/Company/Deal/Quote/Contract
// List's row-building loop will need, grouped by rule.EntityType, so the loop
// itself does zero further queries.
func (h *NotificationLogHandler) resolveEntities(logs []models.NotificationLog, ruleByID map[uint]models.NotificationRule) resolvedEntities {
	var prospectIDs, companyIDs, dealIDs, quoteIDs, contractIDs []uint
	for _, l := range logs {
		rule, ok := ruleByID[l.RuleID]
		if !ok {
			continue
		}
		switch rule.EntityType {
		case models.NotificationEntityProspect:
			prospectIDs = append(prospectIDs, l.EntityID)
		case models.NotificationEntityCompany:
			companyIDs = append(companyIDs, l.EntityID)
		case models.NotificationEntityDeal:
			dealIDs = append(dealIDs, l.EntityID)
		case models.NotificationEntityQuote:
			quoteIDs = append(quoteIDs, l.EntityID)
		case models.NotificationEntityContract:
			contractIDs = append(contractIDs, l.EntityID)
		}
	}

	resolved := resolvedEntities{
		prospects:        map[uint]models.Prospect{},
		companies:        map[uint]models.Company{},
		companyDealOwner: map[uint]*uint{},
		deals:            map[uint]models.Deal{},
		// dealIDForQuote/dealIDForContract are assigned below via
		// dealIDsFor, which returns a non-nil map (empty when there's
		// nothing to resolve) — no need to pre-initialize them here too.
	}

	if len(prospectIDs) > 0 {
		var prospects []models.Prospect
		h.DB.Where("id IN ?", prospectIDs).Find(&prospects)
		for _, p := range prospects {
			resolved.prospects[p.ID] = p
		}
	}

	if len(companyIDs) > 0 {
		var companies []models.Company
		h.DB.Where("id IN ?", companyIDs).Find(&companies)
		for _, co := range companies {
			resolved.companies[co.ID] = co
		}

		// One query for "most recent Deal per Company" across every Company
		// firing at once (ordered so the first row seen per company_id is the
		// newest, since Go map assignment below only keeps the first),
		// replacing what used to be a separate `ORDER BY created_at DESC
		// LIMIT 1` query per Company row. `id DESC` breaks a created_at tie
		// (e.g. Deals bulk-imported/seeded in the same transaction, so their
		// timestamps are identical) the same deterministic way the old
		// per-Company query already did — Postgres' own row order for equal
		// ORDER BY keys isn't otherwise guaranteed to stay stable between
		// this batched query and that one.
		var recentDeals []models.Deal
		h.DB.Where("company_id IN ?", companyIDs).Order("company_id, created_at DESC, id DESC").Find(&recentDeals)
		for _, d := range recentDeals {
			if _, seen := resolved.companyDealOwner[d.CompanyID]; !seen {
				resolved.companyDealOwner[d.CompanyID] = d.AssignedTo
			}
		}
	}

	// Quote/Contract both resolve down to their owning Deal via one extra
	// id hop (Quote.DealID/Contract.DealID) — dealIDsFor shares that "batch-
	// load by id, extract a DealID per row" shape between them instead of
	// two near-identical copies of the same loop.
	resolved.dealIDForQuote = dealIDsFor(h.DB, quoteIDs, func(q models.Quote) (uint, uint) { return q.ID, q.DealID })
	resolved.dealIDForContract = dealIDsFor(h.DB, contractIDs, func(ct models.Contract) (uint, uint) { return ct.ID, ct.DealID })
	for _, dealID := range resolved.dealIDForQuote {
		dealIDs = append(dealIDs, dealID)
	}
	for _, dealID := range resolved.dealIDForContract {
		dealIDs = append(dealIDs, dealID)
	}

	if len(dealIDs) > 0 {
		var deals []models.Deal
		h.DB.Where("id IN ?", dealIDs).Find(&deals)
		for _, d := range deals {
			resolved.deals[d.ID] = d
		}
	}

	return resolved
}

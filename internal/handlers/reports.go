package handlers

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ReportHandler struct {
	DB *gorm.DB
}

func NewReportHandler(db *gorm.DB) *ReportHandler {
	return &ReportHandler{DB: db}
}

type leadSourceConversion struct {
	Source         models.LeadSource `json:"source"`
	Total          int64             `json:"total"`
	Qualified      int64             `json:"qualified"`
	ConversionRate float64           `json:"conversion_rate"`
}

// LeadSourceConversion — GET /reports/lead-source-conversion?assigned_to=&date_from=&date_to=
// (Sales Manager/Admin, route-gated). FR-CRM-054, FR-CRM-055 (rep filter). Lead
// has no Company FK (only a free-text company_name), so there's no company_tag
// filter here — that only applies to Deal-based reports.
func (h *ReportHandler) LeadSourceConversion(c *fiber.Ctx) error {
	query := h.DB.Model(&models.Lead{})
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("date_from"); v != "" {
		query = query.Where("created_at >= ?", v)
	}
	if v := c.Query("date_to"); v != "" {
		query = query.Where("created_at <= ?", v)
	}

	var rows []struct {
		Source    models.LeadSource
		Total     int64
		Qualified int64
	}
	err := query.
		Select("source, count(*) as total, count(*) FILTER (WHERE status = 'Qualified') as qualified").
		Group("source").
		Scan(&rows).Error
	if err != nil {
		return utils.Internal(c, "Failed to compute lead source conversion")
	}

	result := make([]leadSourceConversion, 0, len(rows))
	for _, r := range rows {
		rate := 0.0
		if r.Total > 0 {
			rate = float64(r.Qualified) / float64(r.Total) * 100
		}
		result = append(result, leadSourceConversion{Source: r.Source, Total: r.Total, Qualified: r.Qualified, ConversionRate: rate})
	}
	return utils.OK(c, result)
}

type customerByProductStatus struct {
	CompanyID   uint                         `json:"company_id"`
	CompanyName string                       `json:"company_name"`
	ProductID   uint                         `json:"product_id"`
	Status      models.CustomerProductStatus `json:"status"`
	StartDate   string                       `json:"start_date"`
}

// CustomersByProductStatus — GET /reports/customers-by-product-status?product_id=&status=&company_tag=
// (Sales Manager/Admin, route-gated). FR-CRM-056, FR-CRM-055 (company-tag filter).
func (h *ReportHandler) CustomersByProductStatus(c *fiber.Ctx) error {
	query := h.DB.Model(&models.CustomerProduct{}).
		Select("customer_products.company_id, companies.name as company_name, customer_products.product_id, customer_products.status, customer_products.start_date").
		Joins("JOIN companies ON companies.id = customer_products.company_id")

	if v := c.Query("product_id"); v != "" {
		query = query.Where("customer_products.product_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("customer_products.status = ?", v)
	}
	if v := c.Query("company_tag"); v != "" {
		query = query.Where("companies.tags && ARRAY[?]::text[]", v)
	}

	// Initialized non-nil (not `var rows []T`) so zero matching rows marshals
	// to `[]` in the JSON response, not `null` — GORM's Scan never touches
	// the destination reflect value at all when no rows match, so a nil
	// starting slice stays nil straight through to json.Marshal, and the
	// frontend's `.map()`/`.length` on the response blows up on a null body.
	rows := []customerByProductStatus{}
	if err := query.Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to compute customers by product status")
	}
	return utils.OK(c, rows)
}

type winLossReasonRow struct {
	Reason string  `json:"reason"` // "won", or a models.LostReason value for lost deals
	Count  int64   `json:"count"`
	Value  float64 `json:"value"`
}

// WinLossReasons — GET /reports/win-loss-reasons?date_from=&date_to=&assigned_to=
// (Sales Manager/Admin, route-gated). FR-CRM-093. Every closed Deal (won or
// lost), grouped by "won" or its lost_reason code (models.LostReason) —
// answers "why are we losing deals," not just the win-rate number the
// dashboard already shows. A lost Deal missing lost_reason (shouldn't
// happen given FR-CRM-024's required-on-Lost validation, but tolerated
// defensively) falls into "other" rather than being silently dropped.
func (h *ReportHandler) WinLossReasons(c *fiber.Ctx) error {
	query := h.DB.Model(&models.Deal{}).Where("status IN ('won', 'lost')")
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("date_from"); v != "" {
		query = query.Where("created_at >= ?", v)
	}
	if v := c.Query("date_to"); v != "" {
		query = query.Where("created_at <= ?", v)
	}

	// Non-nil starting slice — see the comment on CustomersByProductStatus's
	// identical `rows := []T{}` above for why this matters.
	rows := []winLossReasonRow{}
	err := query.
		Select(`CASE WHEN status = 'won' THEN 'won' ELSE COALESCE(lost_reason, 'other') END as reason,
			count(*) as count, COALESCE(SUM(value), 0) as value`).
		Group("reason").
		Scan(&rows).Error
	if err != nil {
		return utils.Internal(c, "Failed to compute win/loss reasons")
	}
	return utils.OK(c, rows)
}

type stalledDealRow struct {
	DealID         uint      `json:"deal_id"`
	Title          string    `json:"title"`
	CompanyName    string    `json:"company_name"`
	Stage          string    `json:"stage"`
	Value          float64   `json:"value"`
	AssignedTo     *uint     `json:"assigned_to"`
	LastActivityAt time.Time `json:"last_activity_at"`
	DaysStalled    int       `json:"days_stalled"`
}

// StalledDeals — GET /reports/stalled-deals?min_days=&assigned_to=
// (Sales Manager/Admin, route-gated). FR-CRM-094. Open Deals with no
// logged Activity (or, if none ever logged, since creation) for at least
// min_days (default 14) — surfaces deals quietly going cold in the
// pipeline rather than actively Lost. last_activity_at is
// COALESCE(MAX(activities.created_at), deals.created_at) via a LEFT JOIN,
// using the same (related_type, related_id) composite index Activity
// already carries for exactly this access pattern (see activity.go).
// min_days filtering happens in Go after the query rather than a SQL
// HAVING, since it's a simple post-filter over an already-small result set
// (open deals only) and keeps the cutoff-vs-now comparison in one place
// with the days_stalled calculation below it.
func (h *ReportHandler) StalledDeals(c *fiber.Ctx) error {
	minDays := 14
	if v := c.Query("min_days"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			minDays = parsed
		}
	}

	query := h.DB.Table("deals").
		Select(`deals.id as deal_id, deals.title, companies.name as company_name, deals.stage,
			deals.value, deals.assigned_to,
			COALESCE(MAX(activities.created_at), deals.created_at) as last_activity_at`).
		Joins("JOIN companies ON companies.id = deals.company_id").
		Joins("LEFT JOIN activities ON activities.related_type = 'deal' AND activities.related_id = deals.id").
		Where("deals.status = ? AND deals.deleted_at IS NULL", models.DealStatusOpen).
		Group("deals.id, deals.title, companies.name, deals.stage, deals.value, deals.assigned_to, deals.created_at")

	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("deals.assigned_to = ?", v)
	}

	var rows []stalledDealRow
	if err := query.Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to compute stalled deals")
	}

	cutoff := time.Now().AddDate(0, 0, -minDays)
	result := make([]stalledDealRow, 0, len(rows))
	for _, r := range rows {
		if r.LastActivityAt.After(cutoff) {
			continue
		}
		r.DaysStalled = int(time.Since(r.LastActivityAt).Hours() / 24)
		result = append(result, r)
	}
	return utils.OK(c, result)
}

type outstandingBalanceRow struct {
	DealID            uint    `json:"deal_id"`
	DealTitle         string  `json:"deal_title"`
	CompanyName       string  `json:"company_name"`
	DealValue         float64 `json:"deal_value"`
	PaidAmount        float64 `json:"paid_amount"`
	OutstandingAmount float64 `json:"outstanding_amount"`
}

// OutstandingBalance — GET /reports/outstanding-balance?company_tag=&assigned_to=
// (Sales Manager/Admin, route-gated). FR-CRM-095. Won Deals whose recorded
// Payments sum to less than the Deal's value — every row is money still
// owed. Payment has no due_date field (only paid_at, when an installment
// was actually received — api-system-spec.md §7.3), so this can't be
// bucketed into 30/60/90-day aging; it's a flat "who still owes what" list
// until that field exists.
func (h *ReportHandler) OutstandingBalance(c *fiber.Ctx) error {
	query := h.DB.Table("deals").
		Select(`deals.id as deal_id, deals.title as deal_title, companies.name as company_name,
			deals.value as deal_value, COALESCE(SUM(payments.amount), 0) as paid_amount,
			deals.value - COALESCE(SUM(payments.amount), 0) as outstanding_amount`).
		Joins("JOIN companies ON companies.id = deals.company_id").
		Joins("LEFT JOIN payments ON payments.deal_id = deals.id").
		Where("deals.status = ? AND deals.deleted_at IS NULL", models.DealStatusWon).
		Group("deals.id, deals.title, companies.name, deals.value").
		Having("deals.value - COALESCE(SUM(payments.amount), 0) > 0")

	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("deals.assigned_to = ?", v)
	}
	if v := c.Query("company_tag"); v != "" {
		query = query.Where("companies.tags && ARRAY[?]::text[]", v)
	}

	// Non-nil starting slice — see the comment on CustomersByProductStatus's
	// identical `rows := []T{}` above for why this matters.
	rows := []outstandingBalanceRow{}
	if err := query.Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to compute outstanding balance")
	}
	return utils.OK(c, rows)
}

type quoteExpiringSoonRow struct {
	QuoteID      uint    `json:"quote_id"`
	DealID       uint    `json:"deal_id"`
	DealTitle    string  `json:"deal_title"`
	CompanyName  string  `json:"company_name"`
	ValidityDate string  `json:"validity_date"`
	TotalValue   float64 `json:"total_value"`
}

// QuotesExpiringSoon — GET /reports/quotes-expiring-soon?within_days=
// (Sales Manager/Admin, route-gated). FR-CRM-096. Sent quotes (not yet
// Accepted/Rejected) whose validity_date falls within the next
// within_days (default 7) — a forward-looking "needs a follow-up before
// it lapses" view, the mirror image of Quote.EffectiveStatus's
// already-expired check. validity_date is free-text (RFC3339 or a bare
// date, same dual-format tolerance as EffectiveStatus), so it's parsed in
// Go rather than SQL.
func (h *ReportHandler) QuotesExpiringSoon(c *fiber.Ctx) error {
	withinDays := 7
	if v := c.Query("within_days"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			withinDays = parsed
		}
	}

	var quotes []models.Quote
	if err := h.DB.Where("status = ?", models.QuoteStatusSent).Find(&quotes).Error; err != nil {
		return utils.Internal(c, "Failed to compute quotes expiring soon")
	}

	now := time.Now()
	deadline := now.AddDate(0, 0, withinDays)
	byDeal := map[uint][]models.Quote{}
	for _, q := range quotes {
		if q.ValidityDate == nil || *q.ValidityDate == "" {
			continue
		}
		validUntil, err := time.Parse(time.RFC3339, *q.ValidityDate)
		if err != nil {
			validUntil, err = time.Parse("2006-01-02", *q.ValidityDate)
			if err != nil {
				continue
			}
		}
		if validUntil.Before(now) || validUntil.After(deadline) {
			continue
		}
		byDeal[q.DealID] = append(byDeal[q.DealID], q)
	}

	result := make([]quoteExpiringSoonRow, 0)
	if len(byDeal) == 0 {
		return utils.OK(c, result)
	}

	dealIDs := make([]uint, 0, len(byDeal))
	for dealID := range byDeal {
		dealIDs = append(dealIDs, dealID)
	}
	var deals []models.Deal
	h.DB.Where("id IN ?", dealIDs).Find(&deals)
	dealByID := make(map[uint]models.Deal, len(deals))
	companyIDs := make([]uint, 0, len(deals))
	for _, d := range deals {
		dealByID[d.ID] = d
		companyIDs = append(companyIDs, d.CompanyID)
	}
	var companies []models.Company
	h.DB.Where("id IN ?", companyIDs).Find(&companies)
	companyNameByID := make(map[uint]string, len(companies))
	for _, comp := range companies {
		companyNameByID[comp.ID] = comp.Name
	}

	for dealID, dealQuotes := range byDeal {
		deal, ok := dealByID[dealID]
		if !ok {
			continue
		}
		for _, q := range dealQuotes {
			total := 0.0
			for _, item := range q.Items {
				total += item.Qty * item.Price
			}
			result = append(result, quoteExpiringSoonRow{
				QuoteID: q.ID, DealID: dealID, DealTitle: deal.Title,
				CompanyName: companyNameByID[deal.CompanyID], ValidityDate: *q.ValidityDate, TotalValue: total,
			})
		}
	}
	return utils.OK(c, result)
}

type contractStuckRow struct {
	ContractID   uint   `json:"contract_id"`
	DealID       uint   `json:"deal_id"`
	DealTitle    string `json:"deal_title"`
	CompanyName  string `json:"company_name"`
	Status       string `json:"status"`
	DaysInStatus int    `json:"days_in_status"`
}

// ContractsStuck — GET /reports/contracts-stuck?min_days=
// (Sales Manager/Admin, route-gated). FR-CRM-097. Contracts sitting in
// Draft or Sent for at least min_days (default 14) without being signed.
// Contract has no start/end date to track true "expiration" by (only
// signed_date, set once actually signed) — this instead surfaces
// contracts stalling before signature, the contract-side equivalent of
// StalledDeals above.
func (h *ReportHandler) ContractsStuck(c *fiber.Ctx) error {
	minDays := 14
	if v := c.Query("min_days"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			minDays = parsed
		}
	}
	cutoff := time.Now().AddDate(0, 0, -minDays)

	query := h.DB.Table("contracts").
		Select(`contracts.id as contract_id, contracts.deal_id, deals.title as deal_title,
			companies.name as company_name, contracts.status, contracts.updated_at`).
		Joins("JOIN deals ON deals.id = contracts.deal_id").
		Joins("JOIN companies ON companies.id = deals.company_id").
		Where("contracts.status IN ('draft', 'sent') AND contracts.updated_at <= ? AND deals.deleted_at IS NULL", cutoff)

	var rows []struct {
		ContractID  uint
		DealID      uint
		DealTitle   string
		CompanyName string
		Status      string
		UpdatedAt   time.Time
	}
	if err := query.Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to compute contracts stuck")
	}

	result := make([]contractStuckRow, 0, len(rows))
	for _, r := range rows {
		result = append(result, contractStuckRow{
			ContractID: r.ContractID, DealID: r.DealID, DealTitle: r.DealTitle,
			CompanyName: r.CompanyName, Status: r.Status,
			DaysInStatus: int(time.Since(r.UpdatedAt).Hours() / 24),
		})
	}
	return utils.OK(c, result)
}

type projectAtRiskRow struct {
	ProjectID     uint   `json:"project_id"`
	Name          string `json:"name"`
	CompanyID     uint   `json:"company_id"`
	CompanyName   string `json:"company_name"`
	Status        string `json:"status"`
	TargetEndDate string `json:"target_end_date"`
	DaysOverdue   int    `json:"days_overdue"`
}

// ProjectsAtRisk — GET /reports/projects-at-risk (Sales Manager/Admin,
// route-gated). FR-CRM-098. Projects whose target_end_date has already
// passed but aren't Completed or Cancelled — the delivery-side equivalent
// of StalledDeals, for whoever owns customer-delivery visibility (§3.7,
// FR-CRM-071).
func (h *ReportHandler) ProjectsAtRisk(c *fiber.Ctx) error {
	query := h.DB.Table("projects").
		Select(`projects.id as project_id, projects.name, projects.company_id, companies.name as company_name,
			projects.status, projects.target_end_date`).
		Joins("JOIN companies ON companies.id = projects.company_id").
		Where("projects.target_end_date IS NOT NULL AND projects.target_end_date < ? AND projects.status NOT IN ('Completed', 'Cancelled') AND projects.deleted_at IS NULL", time.Now())

	var rows []struct {
		ProjectID     uint
		Name          string
		CompanyID     uint
		CompanyName   string
		Status        string
		TargetEndDate time.Time
	}
	if err := query.Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to compute projects at risk")
	}

	result := make([]projectAtRiskRow, 0, len(rows))
	for _, r := range rows {
		result = append(result, projectAtRiskRow{
			ProjectID: r.ProjectID, Name: r.Name, CompanyID: r.CompanyID, CompanyName: r.CompanyName, Status: r.Status,
			TargetEndDate: r.TargetEndDate.Format("2006-01-02"),
			DaysOverdue:   int(time.Since(r.TargetEndDate).Hours() / 24),
		})
	}
	return utils.OK(c, result)
}

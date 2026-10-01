package handlers

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/overview"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// pipeline_overview.go — GET /pipeline/overview (FR-CRM-123). HTTP only:
// parses the date window, filters and card limit, then hands off to
// internal/overview, which the weekly digest email shares.

type PipelineOverviewHandler struct {
	DB *gorm.DB
}

func NewPipelineOverviewHandler(db *gorm.DB) *PipelineOverviewHandler {
	return &PipelineOverviewHandler{DB: db}
}

// resolveOverviewWindow reads the date range (dateRangeQuery) and returns
// the current window plus the equal-length window right before it, which the
// summary strip compares against. A missing bound defaults to the last 7
// days, today included.
func resolveOverviewWindow(c *fiber.Ctx) (cur, prev overview.Window, err error) {
	r, err := dateRangeQuery(c)
	if err != nil {
		return overview.Window{}, overview.Window{}, err
	}
	y, m, d := time.Now().Date()
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, time.Local)
	cur = overview.Window{From: tomorrow.AddDate(0, 0, -7), To: tomorrow}
	if r.From != nil {
		cur.From = *r.From
	}
	if r.ToExclusive != nil {
		cur.To = *r.ToExclusive
	}
	if !cur.From.Before(cur.To) {
		return overview.Window{}, overview.Window{}, utils.ReversedDateRange("date_from", "date_to")
	}
	return cur, overview.PreviousWindow(cur), nil
}

// Overview — GET /pipeline/overview?date_from=&date_to=&assigned_to=&source=&business_unit=&tag=&search=&card_limit=
// salesPipelineRoles-gated (Admin/Sales Rep/Sales Manager/Marketing).
func (h *PipelineOverviewHandler) Overview(c *fiber.Ctx) error {
	cur, prev, err := resolveOverviewWindow(c)
	if err != nil {
		return reportError(c, err, "")
	}
	limit := overview.DefaultCardLimit
	if v := c.Query("card_limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return utils.ValidationError(c, "card_limit is invalid", map[string][]string{"card_limit": {"must be a non-negative integer"}})
		}
		limit = min(n, overview.MaxCardLimit)
	}
	f := overview.Filters{
		AssignedTo: c.Query("assigned_to"), Source: c.Query("source"),
		BusinessUnit: c.Query("business_unit"), Tag: c.Query("tag"), Search: c.Query("search"),
	}
	payload, err := overview.Build(h.DB, f, cur, prev, limit)
	if err != nil {
		return utils.Internal(c, "Failed to load pipeline overview")
	}
	return utils.OK(c, payload)
}

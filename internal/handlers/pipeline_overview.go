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

// resolveOverviewWindow reads date_from/date_to (YYYY-MM-DD, both inclusive)
// and returns the current window plus the equal-length window right before
// it, which the summary strip compares against. Defaults to the last 7 days.
func resolveOverviewWindow(c *fiber.Ctx) (cur, prev overview.Window, fields map[string][]string) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	from, to := today.AddDate(0, 0, -6), today
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"date_from", &from}, {"date_to", &to}} {
		if v := c.Query(p.name); v != "" {
			t, err := time.ParseInLocation("2006-01-02", v, time.Local)
			if err != nil {
				return overview.Window{}, overview.Window{}, map[string][]string{p.name: {"must be a valid YYYY-MM-DD date"}}
			}
			*p.dst = t
		}
	}
	if to.Before(from) {
		return overview.Window{}, overview.Window{}, map[string][]string{"date_to": {"must be on or after date_from"}}
	}
	cur = overview.Window{From: from, To: to.AddDate(0, 0, 1)}
	return cur, overview.PreviousWindow(cur), nil
}

// Overview — GET /pipeline/overview?date_from=&date_to=&assigned_to=&source=&business_unit=&tag=&search=&card_limit=
// salesPipelineRoles-gated (Admin/Sales Rep/Sales Manager/Marketing).
func (h *PipelineOverviewHandler) Overview(c *fiber.Ctx) error {
	cur, prev, fields := resolveOverviewWindow(c)
	if fields != nil {
		return utils.ValidationError(c, "Invalid date range", fields)
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

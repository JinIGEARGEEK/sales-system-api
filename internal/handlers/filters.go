package handlers

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// This file centralizes the query-param filter logic shared between each
// resource's List handler and its ExportHandler counterpart. Previously each
// export handler duplicated its List handler's filter block near-verbatim,
// and the two had already drifted (e.g. Deals export was missing List's
// assigned_to=unassigned special case, Companies export's search didn't
// cover website like List's did) — a filter added to one silently wouldn't
// apply to the other. Single source of truth per resource fixes that for good.

// applyCompanyFilters applies status/industry/tag/search filters shared by
// CompanyHandler.List and ExportHandler.Companies.
func applyCompanyFilters(query *gorm.DB, c *fiber.Ctx) *gorm.DB {
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("industry"); v != "" {
		query = query.Where("industry = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		query = query.Where("? = ANY(tags)", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		query = query.Where("name ILIKE ? OR website ILIKE ?", like, like)
	}
	// stale_days — only companies with no company-scoped Activity (see
	// company_activity.go's withLastActivityAt for the same "related_type =
	// 'company'" definition) at or after the cutoff, i.e. last_activity_at is
	// NULL or older than stale_days. Expressed as a NOT EXISTS rather than
	// relying on withLastActivityAt's joined alias, so this filter works
	// standalone here (and in ExportHandler.Companies, which shares this
	// function but never joins the activity subquery itself).
	if v := c.Query("stale_days"); v != "" {
		if days, err := strconv.Atoi(v); err == nil {
			cutoff := time.Now().AddDate(0, 0, -days)
			query = query.Where(
				"NOT EXISTS (SELECT 1 FROM activities WHERE activities.related_type = ? AND activities.related_id = companies.id AND activities.created_at >= ?)",
				models.RelatedTypeCompany, cutoff,
			)
		}
	}
	// has_won_deal — "true"/"false" string param; only companies with (or
	// without) at least one Deal at status = 'won'.
	if v := c.Query("has_won_deal"); v != "" {
		if hasWonDeal, err := strconv.ParseBool(v); err == nil {
			exists := "EXISTS (SELECT 1 FROM deals WHERE deals.company_id = companies.id AND deals.status = ?)"
			if !hasWonDeal {
				exists = "NOT " + exists
			}
			query = query.Where(exists, models.DealStatusWon)
		}
	}
	return query
}

// applyContactFilters applies company_id/status/tag/search filters shared by
// ContactHandler.List and ExportHandler.Contacts.
func applyContactFilters(query *gorm.DB, c *fiber.Ctx) *gorm.DB {
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		query = query.Where("? = ANY(tags)", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		query = query.Where("name ILIKE ? OR email ILIKE ?", like, like)
	}
	return query
}

// applyDealFilters applies stage/status/company_id/assigned_to/business_unit/
// channel/search filters shared by DealHandler.List and ExportHandler.Deals.
func applyDealFilters(query *gorm.DB, c *fiber.Ctx) *gorm.DB {
	if v := c.Query("stage"); v != "" {
		query = query.Where("stage = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	if v := c.Query("assigned_to"); v == "unassigned" {
		query = query.Where("assigned_to IS NULL")
	} else if v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("business_unit"); v != "" {
		query = query.Where("business_unit = ?", v)
	}
	if v := c.Query("channel"); v != "" {
		query = query.Where("channel = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("title ILIKE ?", "%"+v+"%")
	}
	return query
}

// applyProductFilters applies category/search filters shared by
// ProductHandler.List and ExportHandler.Products.
func applyProductFilters(query *gorm.DB, c *fiber.Ctx) *gorm.DB {
	if v := c.Query("category"); v != "" {
		query = query.Where("category = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("name ILIKE ?", "%"+v+"%")
	}
	return query
}

// applyProjectFilters applies status/company_id filters shared by
// ProjectHandler.List and ExportHandler.Projects.
func applyProjectFilters(query *gorm.DB, c *fiber.Ctx) *gorm.DB {
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	return query
}

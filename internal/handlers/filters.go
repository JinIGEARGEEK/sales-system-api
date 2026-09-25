package handlers

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// This file centralizes the query-param filter logic shared between each
// resource's List handler and its ExportHandler counterpart. Previously each
// export handler duplicated its List handler's filter block near-verbatim,
// and the two had already drifted (e.g. Deals export was missing List's
// assigned_to=unassigned special case, Companies export's search didn't
// cover website like List's did) — a filter added to one silently wouldn't
// apply to the other. Single source of truth per resource fixes that for good.

// tagFilter matches a row whose Tags array contains v, case-insensitively.
// Lowercasing v (rather than unnest+LOWER()-ing every row's tags) is what
// keeps this a plain `= ANY(tags)` lookup — usable by the GIN tags index —
// instead of a per-row scan; it's safe because normalizeTags (companies.go)
// already lowercases every tag at write time, and database.go's
// backfillLowercaseTags normalized every pre-existing row the same way.
// Shared by applyCompanyFilters and applyContactFilters, which both store
// Tags the same way (pq.StringArray).
func tagFilter(query *gorm.DB, v string) *gorm.DB {
	return query.Where("? = ANY(tags)", strings.ToLower(v))
}

// applyCompanyFilters applies status/industry/tag/search filters shared by
// CompanyHandler.List and ExportHandler.Companies.
func applyCompanyFilters(query *gorm.DB, c *fiber.Ctx) *gorm.DB {
	if v := c.Query("status"); v != "" {
		// Case-insensitive: Status is meant to be the canonical lowercase
		// active/archived (now enforced on write, see normalizeCompanyStatus),
		// but older/imported rows may not be, so match defensively rather
		// than silently excluding them.
		query = query.Where("LOWER(status) = LOWER(?)", v)
	}
	if v := c.Query("industry"); v != "" {
		// Case-insensitive: industry is free text that auto-registers
		// whatever casing it's typed with (utils.EnsureActiveIndustry), so
		// "Tech" and "tech" can both exist on stored rows even though
		// they're meant to be the same industry. Backed by an expression
		// index (database.go) since this can't use industry's plain index.
		query = query.Where("LOWER(industry) = LOWER(?)", v)
	}
	if v := c.Query("tag"); v != "" {
		query = tagFilter(query, v)
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
		// Case-insensitive: Status is meant to be the canonical lowercase
		// active/archived (now enforced on write, see
		// normalizeActiveArchivedStatus), but older/imported rows may not
		// be, so match defensively rather than silently excluding them.
		query = query.Where("LOWER(status) = LOWER(?)", v)
	}
	if v := c.Query("tag"); v != "" {
		query = tagFilter(query, v)
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

// relatedRecordNameMatch is a WHERE fragment matching a row whose
// polymorphic (related_type, related_id) pair points at a record whose
// display name ILIKEs the bound pattern — the same label the frontend shows
// in a Task/Activity row's "Related" column (Deal title; Contact/Company/
// Prospect/Lead name). Needs the pattern bound five times, once per
// subquery; see relatedRecordNameArgs. `table` qualifies the outer row's
// columns so this composes safely with other joins.
func relatedRecordNameMatch(table string) string {
	col := func(c string) string { return table + "." + c }
	sub := func(relatedType, target, nameCol string) string {
		return "(" + col("related_type") + " = '" + relatedType + "' AND EXISTS (SELECT 1 FROM " + target +
			" r WHERE r.id = " + col("related_id") + " AND r." + nameCol + " ILIKE ?))"
	}
	return sub("deal", "deals", "title") + " OR " +
		sub("contact", "contacts", "name") + " OR " +
		sub("company", "companies", "name") + " OR " +
		sub("prospect", "prospects", "name") + " OR " +
		sub("lead", "leads", "name")
}

func relatedRecordNameArgs(like string) []interface{} {
	return []interface{}{like, like, like, like, like}
}

// parseTimeBound accepts either a full RFC 3339 timestamp (what the Tasks
// page sends: the viewer's local midnight, with offset, so "today" means the
// viewer's today rather than the server's) or a bare YYYY-MM-DD date
// (interpreted as UTC midnight).
func parseTimeBound(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

// applyTaskFilters applies the GET /tasks filter block:
//   - related_type+related_id together (a detail page's own Tasks tab), or
//     related_type alone (every task linked to that kind of record);
//   - status, assigned_to (a user id, or "unassigned"), campaign_id;
//   - search: title/description, or the linked record's display name;
//   - business_unit: tasks whose linked Deal/Prospect has that business
//     unit (Tasks have none of their own; Contact/Company-linked tasks never
//     match);
//   - due_from (inclusive) / due_before (exclusive): RFC 3339 or YYYY-MM-DD
//     bounds on due_date — the Tasks page's Overdue/Today/Upcoming groups.
//
// status=pending must keep working without related_type/related_id for the
// dashboard widget. Returns a non-nil error (a 400 for the caller) only for
// an unparseable due_from/due_before.
func applyTaskFilters(query *gorm.DB, c *fiber.Ctx) (*gorm.DB, error) {
	relatedType := c.Query("related_type")
	relatedID := c.Query("related_id")
	if relatedType != "" && relatedID != "" {
		query = query.Where("tasks.related_type = ? AND tasks.related_id = ?", relatedType, relatedID)
	} else if relatedType != "" {
		query = query.Where("tasks.related_type = ?", relatedType)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("tasks.status = ?", v)
	}
	if v := c.Query("assigned_to"); v == "unassigned" {
		query = query.Where("tasks.assigned_to IS NULL")
	} else if v != "" {
		query = query.Where("tasks.assigned_to = ?", v)
	}
	if v := c.Query("campaign_id"); v != "" {
		query = query.Where("tasks.campaign_id = ?", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		args := append([]interface{}{like, like}, relatedRecordNameArgs(like)...)
		query = query.Where("tasks.title ILIKE ? OR tasks.description ILIKE ? OR "+relatedRecordNameMatch("tasks"), args...)
	}
	if v := c.Query("business_unit"); v != "" {
		query = query.Where(
			"(tasks.related_type = 'deal' AND tasks.related_id IN (SELECT id FROM deals WHERE business_unit = ?)) OR "+
				"(tasks.related_type = 'prospect' AND tasks.related_id IN (SELECT id FROM prospects WHERE business_unit = ?))",
			v, v)
	}
	if v := c.Query("due_from"); v != "" {
		t, err := parseTimeBound(v)
		if err != nil {
			return query, err
		}
		query = query.Where("tasks.due_date >= ?", t)
	}
	if v := c.Query("due_before"); v != "" {
		t, err := parseTimeBound(v)
		if err != nil {
			return query, err
		}
		query = query.Where("tasks.due_date < ?", t)
	}
	return query, nil
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

// applyLeadLikeFilters applies the status/source/assigned_to/company_id/
// search/exclude_converted filter block shared by LeadHandler.List and
// ProspectHandler.List — both resources have an identical filter shape;
// only the table name (needed for the company_id column-qualification and
// the Company-name join/sort helpers below) and the "already converted"
// column name differ between them. Returns the query plus whether a Company
// join is needed for sort (see utils.ApplyNullableCompanySearch) — the
// caller still has to apply the final ORDER BY itself since that differs
// slightly by needsCompanyJoin.
func applyLeadLikeFilters(query *gorm.DB, c *fiber.Ctx, table, excludeConvertedColumn string) (*gorm.DB, bool, string) {
	// Every filter column here is qualified with table (not just company_id,
	// which already was) — status in particular collides with Company's own
	// `status` column: utils.ApplyNullableCompanySearch below LEFT JOINs
	// companies whenever search or a company_name sort is in play, and an
	// unqualified `status = ?` alongside that join is ambiguous to Postgres
	// ("column reference \"status\" is ambiguous") wherever both happen to
	// be requested on the same call (e.g. `?status=New&search=acme`) —
	// crashing that request with a 500 rather than a bug isolated to sort.
	// source/assigned_to don't actually collide with any companies column
	// today, but qualifying them the same way is free and avoids the same
	// class of bug if that ever changes.
	if v := c.Query("status"); v != "" {
		query = query.Where(table+".status = ?", v)
	}
	if v := c.Query("source"); v != "" {
		query = query.Where(table+".source = ?", v)
	}
	if v := c.Query("assigned_to"); v == "unassigned" {
		query = query.Where(table + ".assigned_to IS NULL")
	} else if v != "" {
		query = query.Where(table+".assigned_to = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where(table+".company_id = ?", v)
	}

	sortField := strings.TrimPrefix(c.Query("sort"), "-")
	search := c.Query("search")
	query, needsCompanyJoin := utils.ApplyNullableCompanySearch(query, table, sortField, search)
	if c.Query("exclude_converted") == "true" {
		query = query.Where(table + "." + excludeConvertedColumn + " IS NULL")
	}
	if c.Query("only_converted") == "true" {
		query = query.Where(table + "." + excludeConvertedColumn + " IS NOT NULL")
	}
	return query, needsCompanyJoin, sortField
}

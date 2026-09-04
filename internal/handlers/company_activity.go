package handlers

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// company_activity.go — the shared "last activity" computation for Company
// (dormant-customer / upsell-targeting feature). "Last contacted" for a
// Company is defined as MAX(activities.created_at) WHERE related_type =
// 'company' AND related_id = companies.id — company-scoped Activities ONLY,
// deliberately NOT rolled up from that Company's Deals/Contacts. Computed at
// query time via a LEFT JOIN subquery rather than a stored column, so there's
// no migration/backfill to keep in sync as Activities are added.
//
// Mirrors dashboard.go's existing subquery-aggregate style (see
// industryBreakdown's Joins/Select pattern) rather than introducing a new one.

// withLastActivityAt LEFT JOINs a per-company MAX(activities.created_at)
// subquery onto query, aliased last_company_activity(last_activity_at) —
// callers still need to add "companies.*, last_company_activity.last_activity_at
// as last_activity_at" (or similar) to their own Select; this only attaches
// the join, so it composes with both a single-row Get and a list/Count query
// (an extra unused LEFT JOIN is harmless in a Count).
func withLastActivityAt(query *gorm.DB) *gorm.DB {
	return query.Joins(
		"LEFT JOIN (SELECT related_id, MAX(created_at) as last_activity_at FROM activities WHERE related_type = ? GROUP BY related_id) as last_company_activity ON last_company_activity.related_id = companies.id",
		models.RelatedTypeCompany,
	)
}

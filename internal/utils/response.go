package utils

import (
	"math"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// ListMeta mirrors the frontend's ApiResponse<T> list envelope — api-system-spec.md §1.3.
type ListMeta struct {
	Page      int   `json:"page"`
	PerPage   int   `json:"per_page"`
	Total     int64 `json:"total"`
	TotalPage int   `json:"total_page"`
	Next      *int  `json:"next"`
	Prev      *int  `json:"prev"`
}

func OK(c *fiber.Ctx, data interface{}) error {
	return c.JSON(fiber.Map{"data": data})
}

func Created(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": data})
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// List writes the paginated envelope §1.3 given the page/per_page already applied
// to the query and the total row count before pagination.
func List(c *fiber.Ctx, data interface{}, page, perPage int, total int64) error {
	totalPage := int(math.Ceil(float64(total) / float64(perPage)))
	if totalPage < 1 {
		totalPage = 1
	}

	var next, prev *int
	if page < totalPage {
		n := page + 1
		next = &n
	}
	if page > 1 {
		p := page - 1
		prev = &p
	}

	return c.JSON(fiber.Map{
		"data":       data,
		"page":       page,
		"per_page":   perPage,
		"total":      total,
		"total_page": totalPage,
		"next":       next,
		"prev":       prev,
	})
}

// Pagination reads page/per_page from the query string per §1.4's defaults.
func Pagination(c *fiber.Ctx) (page int, perPage int, offset int) {
	page = c.QueryInt("page", 1)
	perPage = c.QueryInt("per_page", 20)
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 20
	}
	offset = (page - 1) * perPage
	return
}

// ApplySort parses `sort=-created_at` (leading '-' = descending) into a gorm Order
// clause, restricted to an allow-list of sortable columns to prevent SQL injection.
func ApplySort(db *gorm.DB, sortParam string, allowed map[string]bool, defaultSort string) *gorm.DB {
	sortParam = orDefault(sortParam, defaultSort)
	if sortParam == "" {
		return db
	}
	col := sortParam
	desc := false
	if len(sortParam) > 0 && sortParam[0] == '-' {
		desc = true
		col = sortParam[1:]
	}
	if !allowed[col] {
		if defaultSort == "" {
			return db
		}
		col = defaultSort
		desc = false
		if len(col) > 0 && col[0] == '-' {
			desc = true
			col = col[1:]
		}
	}
	if desc {
		return db.Order(col + " DESC")
	}
	return db.Order(col + " ASC")
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ApplyCompanyNameSort handles the "company_name" sort special case shared by
// every resource that belongs to a Company but has no such column of its own
// (Deal, Contact) — the real column lives on the joined companies table, so
// it can't go through ApplySort's plain allow-list. table is the caller's own
// table name (e.g. "deals"), used both to build the FK join condition
// (table.company_id) and to re-narrow the SELECT list back to the caller's
// own columns afterward — without that, SELECT * would also pull the joined
// companies.* columns, which Find can't scan into the caller's model.
//
// Returns the query unchanged, and false, when sortParam doesn't request
// "company_name" — callers fall back to their own ApplySort call in that
// case. Note this always uses an INNER JOIN: it's only reachable for
// resources where the FK is NOT NULL (Deal.CompanyID, Contact.CompanyID), so
// a row can never disappear from the join. Lead's sort-by-company_name case
// is intentionally NOT unified here — Lead.CompanyID is nullable and the join
// is also needed for its "search" filter, so leads.go keeps its own LEFT JOIN
// handling rather than forcing this INNER-JOIN-only helper to cover both.
func ApplyCompanyNameSort(query *gorm.DB, table, sortParam string) (*gorm.DB, bool) {
	sortField := strings.TrimPrefix(sortParam, "-")
	if sortField != "company_name" {
		return query, false
	}
	dir := "ASC"
	if strings.HasPrefix(sortParam, "-") {
		dir = "DESC"
	}
	query = query.Joins("JOIN companies ON companies.id = " + table + ".company_id").
		Order("companies.name " + dir).
		Select(table + ".*")
	return query, true
}

// ApplyNullableCompanySearch is ApplyCompanyNameSort's counterpart for
// resources with a NULLABLE company_id FK that also search/sort by the
// related Company's name — Lead, and now Prospect. It must LEFT JOIN rather
// than ApplyCompanyNameSort's INNER JOIN (only safe for Deal/Contact's NOT
// NULL company_id), or a row with no company at all would vanish from an
// otherwise-unfiltered list/search. It also folds in the search predicate
// itself (ApplyCompanyNameSort doesn't — Deal/Contact's own "search" was
// never company-name-aware), since both call sites need the join for
// exactly the same two reasons at once.
//
// Call this BEFORE Count() — narrowing the SELECT list (done by
// ApplyNullableCompanySort below, after Count()) would break a plain
// COUNT(*) against Postgres ("column table.* does not exist"). Returns the
// query and whether a join was actually added (sortField == "company_name"
// or search != ""); callers only need ApplyNullableCompanySort afterward
// when this returns true.
func ApplyNullableCompanySearch(query *gorm.DB, table, sortField, search string) (*gorm.DB, bool) {
	if sortField != "company_name" && search == "" {
		return query, false
	}
	query = query.Joins("LEFT JOIN companies ON companies.id = " + table + ".company_id")
	if search != "" {
		like := "%" + search + "%"
		query = query.Where(table+".name ILIKE ? OR "+table+".email ILIKE ? OR companies.name ILIKE ?", like, like, like)
	}
	return query, true
}

// ApplyNullableCompanySort narrows the SELECT list back to the caller's own
// columns — required after ApplyNullableCompanySearch's LEFT JOIN so Find
// can scan into the caller's model, since SELECT * would otherwise also pull
// every joined companies.* column — and, if sortField is "company_name",
// orders by the joined Company's name. Only call this when
// ApplyNullableCompanySearch returned true.
func ApplyNullableCompanySort(query *gorm.DB, table, sortParam, sortField string) *gorm.DB {
	query = query.Select(table + ".*")
	if sortField != "company_name" {
		return query
	}
	dir := "ASC"
	if strings.HasPrefix(sortParam, "-") {
		dir = "DESC"
	}
	return query.Order("companies.name " + dir)
}

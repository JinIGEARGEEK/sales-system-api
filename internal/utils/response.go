package utils

import (
	"math"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// ListMeta mirrors the frontend's ApiResponse<T> list envelope — api-system-spec.md §1.3.
type ListMeta struct {
	Page      int  `json:"page"`
	PerPage   int  `json:"per_page"`
	Total     int64 `json:"total"`
	TotalPage int  `json:"total_page"`
	Next      *int `json:"next"`
	Prev      *int `json:"prev"`
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

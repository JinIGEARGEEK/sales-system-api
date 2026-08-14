package handlers

import (
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

// LeadSourceConversion — GET /reports/lead-source-conversion (Sales
// Manager/Admin, route-gated). FR-CRM-054.
func (h *ReportHandler) LeadSourceConversion(c *fiber.Ctx) error {
	var rows []struct {
		Source    models.LeadSource
		Total     int64
		Qualified int64
	}
	err := h.DB.Model(&models.Lead{}).
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

// CustomersByProductStatus — GET /reports/customers-by-product-status?product_id=&status=
// (Sales Manager/Admin, route-gated). FR-CRM-056.
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

	var rows []customerByProductStatus
	if err := query.Scan(&rows).Error; err != nil {
		return utils.Internal(c, "Failed to compute customers by product status")
	}
	return utils.OK(c, rows)
}

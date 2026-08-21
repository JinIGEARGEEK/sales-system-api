package models

import "time"

// Product — api-system-spec.md §8.2.
type Product struct {
	AuditedModel
	Name        string  `gorm:"not null" json:"name"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Price       float64 `gorm:"default:0" json:"price"`
	IsActive    bool    `gorm:"default:true" json:"is_active"`
}

func (Product) TableName() string { return "products" }

type CustomerProductStatus string

const (
	CustomerProductInterested CustomerProductStatus = "Interested"
	CustomerProductTrial      CustomerProductStatus = "Trial"
	CustomerProductActive     CustomerProductStatus = "Active"
	CustomerProductChurned    CustomerProductStatus = "Churned"
)

// ValidCustomerProductStatuses lists every accepted CustomerProductStatus
// value, for handler-layer validation — this is a fixed lifecycle enum
// (mirrors LostReason), not an open-ended categorization list.
var ValidCustomerProductStatuses = []CustomerProductStatus{
	CustomerProductInterested, CustomerProductTrial, CustomerProductActive, CustomerProductChurned,
}

func IsValidCustomerProductStatus(s CustomerProductStatus) bool {
	if s == "" {
		return true
	}
	for _, v := range ValidCustomerProductStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// CustomerProduct — api-system-spec.md §8.2.
type CustomerProduct struct {
	AuditedModel
	CompanyID    uint                  `gorm:"not null;index" json:"company_id"`
	ProductID    uint                  `gorm:"not null;index" json:"product_id"`
	Status       CustomerProductStatus `gorm:"type:varchar(16);default:'Interested'" json:"status"`
	StartDate    time.Time             `json:"start_date"`
	EndDate      *time.Time            `json:"end_date"`
	SourceDealID *uint                 `json:"source_deal_id"`
}

func (CustomerProduct) TableName() string { return "customer_products" }

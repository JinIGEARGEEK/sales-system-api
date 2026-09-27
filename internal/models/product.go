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

// BillingCycle is how often a customer's Product subscription renews.
type BillingCycle string

const (
	BillingCycleMonthly BillingCycle = "monthly"
	BillingCycleYearly  BillingCycle = "yearly"
	BillingCycleOneTime BillingCycle = "one_time"
)

var ValidBillingCycles = []BillingCycle{BillingCycleMonthly, BillingCycleYearly, BillingCycleOneTime}

// IsValidBillingCycle accepts "" (unset) plus each ValidBillingCycles value.
func IsValidBillingCycle(b BillingCycle) bool {
	if b == "" {
		return true
	}
	for _, v := range ValidBillingCycles {
		if v == b {
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
	// RenewalDate is the next date this subscription renews (a calendar
	// date, no time). The customer_product_renewal NotificationRule fires
	// once per RenewalDate value, so rolling it forward a year after
	// renewing re-arms the reminder. Optional; nil never fires.
	RenewalDate *time.Time `gorm:"type:date;index" json:"renewal_date"`
	// BillingCycle/Price describe what the renewal is worth — informational
	// (and shown in the renewal Task), not used to generate any invoice:
	// invoices are issued in FlowAccount, charged via a Contract.
	BillingCycle *BillingCycle `gorm:"type:varchar(16)" json:"billing_cycle"`
	Price        *float64      `json:"price"`
}

func (CustomerProduct) TableName() string { return "customer_products" }

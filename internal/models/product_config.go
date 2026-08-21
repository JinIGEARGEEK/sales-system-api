package models

// ProductCategoryOption is an Admin-configurable Product category list —
// Product.Category was pure free text with no dropdown at all. Same
// row-per-option shape as IndustryOption/LeadSourceOption.
type ProductCategoryOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (ProductCategoryOption) TableName() string { return "product_category_options" }

// DefaultProductCategoryOptions is seeded on first run — a starting point an
// Admin is expected to tune, not a fixed business rule (no prior hardcoded
// list to preserve).
var DefaultProductCategoryOptions = []ProductCategoryOption{
	{Name: "Software", IsActive: true},
	{Name: "Hardware", IsActive: true},
	{Name: "Service", IsActive: true},
	{Name: "Subscription", IsActive: true},
	{Name: "Consulting", IsActive: true},
	{Name: "Other", IsActive: true},
}

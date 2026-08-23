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

// GetName/SetName/GetActive/SetActive satisfy handlers.OptionRow so
// ProductCategoryOptionHandler's CRUD lives in the shared generic handler.
func (o *ProductCategoryOption) GetName() string  { return o.Name }
func (o *ProductCategoryOption) SetName(n string) { o.Name = n }
func (o *ProductCategoryOption) GetActive() bool  { return o.IsActive }
func (o *ProductCategoryOption) SetActive(a bool) { o.IsActive = a }

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

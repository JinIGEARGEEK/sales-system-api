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

// ProductCategoryOption accessor methods — implement handlers.namedOptionModel so
// OptionHandler[T, PT] (internal/handlers/option_crud.go) can generically
// Create/Update/Delete it without a per-type struct literal.
func (o *ProductCategoryOption) GetName() string      { return o.Name }
func (o *ProductCategoryOption) SetName(v string)     { o.Name = v }
func (o *ProductCategoryOption) GetIsActive() bool    { return o.IsActive }
func (o *ProductCategoryOption) SetIsActive(v bool)   { o.IsActive = v }
func (o *ProductCategoryOption) SetCreatedBy(v *uint) { o.CreatedBy = v }
func (o *ProductCategoryOption) SetUpdatedBy(v *uint) { o.UpdatedBy = v }
func (o *ProductCategoryOption) SetDeletedBy(v *uint) { o.DeletedBy = v }

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

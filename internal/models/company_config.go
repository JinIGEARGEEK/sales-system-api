package models

// IndustryOption is an Admin-configurable Company industry — replaces the
// previously frontend-only hardcoded INDUSTRY_OPTIONS list as the source of
// truth for what industries exist, same shape as PipelineStage/
// LeadSourceOption. Company.Industry itself stays a plain string column
// (no Go-level enum) for the same backward-compatibility reason those two
// give.
type IndustryOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (IndustryOption) TableName() string { return "industry_options" }

// GetName/SetName/GetActive/SetActive satisfy handlers.OptionRow so
// IndustryOptionHandler's CRUD lives in the shared generic handler.
func (o *IndustryOption) GetName() string  { return o.Name }
func (o *IndustryOption) SetName(n string) { o.Name = n }
func (o *IndustryOption) GetActive() bool  { return o.IsActive }
func (o *IndustryOption) SetActive(a bool) { o.IsActive = a }

// DefaultIndustryOptions is seeded verbatim from the retired frontend-only
// INDUSTRY_OPTIONS constant (constants/mockData/companies.ts) so existing
// Company rows keep validating unchanged post-migration.
var DefaultIndustryOptions = []IndustryOption{
	{Name: "Technology", IsActive: true},
	{Name: "Retail", IsActive: true},
	{Name: "Manufacturing", IsActive: true},
	{Name: "Healthcare", IsActive: true},
	{Name: "Finance", IsActive: true},
	{Name: "Education", IsActive: true},
}

// CompanySizeOption is an Admin-configurable Company size bucket. Unlike
// IndustryOption, there's no prior hardcoded list to preserve — Size was
// unconstrained free text before this — so DefaultCompanySizeOptions is a
// starting point an Admin is expected to tune, not a fixed business rule
// (same framing as DefaultLeadScoringCriteria).
type CompanySizeOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (CompanySizeOption) TableName() string { return "company_size_options" }

// GetName/SetName/GetActive/SetActive satisfy handlers.OptionRow so
// CompanySizeOptionHandler's CRUD lives in the shared generic handler.
func (o *CompanySizeOption) GetName() string  { return o.Name }
func (o *CompanySizeOption) SetName(n string) { o.Name = n }
func (o *CompanySizeOption) GetActive() bool  { return o.IsActive }
func (o *CompanySizeOption) SetActive(a bool) { o.IsActive = a }

var DefaultCompanySizeOptions = []CompanySizeOption{
	{Name: "1-10", IsActive: true},
	{Name: "11-50", IsActive: true},
	{Name: "51-200", IsActive: true},
	{Name: "201-500", IsActive: true},
	{Name: "501-1000", IsActive: true},
	{Name: "1000+", IsActive: true},
}

// RevenueSizeOption is an Admin-configurable Company revenue bracket. Unlike
// IndustryOption, there's no prior hardcoded list to preserve — RevenueSize was
// unconstrained free text before this — so DefaultRevenueSizeOptions is a
// starting point an Admin is expected to tune, not a fixed business rule
// (same framing as DefaultLeadScoringCriteria).
type RevenueSizeOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (RevenueSizeOption) TableName() string { return "revenue_size_options" }

// GetName/SetName/GetActive/SetActive satisfy handlers.OptionRow so
// RevenueSizeOptionHandler's CRUD lives in the shared generic handler.
func (o *RevenueSizeOption) GetName() string  { return o.Name }
func (o *RevenueSizeOption) SetName(n string) { o.Name = n }
func (o *RevenueSizeOption) GetActive() bool  { return o.IsActive }
func (o *RevenueSizeOption) SetActive(a bool) { o.IsActive = a }

var DefaultRevenueSizeOptions = []RevenueSizeOption{
	{Name: "< 1M THB", IsActive: true},
	{Name: "1M - 5M THB", IsActive: true},
	{Name: "5M - 20M THB", IsActive: true},
	{Name: "20M - 100M THB", IsActive: true},
	{Name: "100M+ THB", IsActive: true},
}

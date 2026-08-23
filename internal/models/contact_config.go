package models

// JobTitleOption is an Admin-configurable Contact job-title/role list —
// Contact.RoleTitle was pure free text with no dropdown at all, same shape
// Company.Size was in before its own IndustryOption/CompanySizeOption
// treatment. Same row-per-option shape as IndustryOption/LeadSourceOption.
type JobTitleOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (JobTitleOption) TableName() string { return "job_title_options" }

// GetName/SetName/GetActive/SetActive satisfy handlers.OptionRow so
// JobTitleOptionHandler's CRUD lives in the shared generic handler.
func (o *JobTitleOption) GetName() string  { return o.Name }
func (o *JobTitleOption) SetName(n string) { o.Name = n }
func (o *JobTitleOption) GetActive() bool  { return o.IsActive }
func (o *JobTitleOption) SetActive(a bool) { o.IsActive = a }

// DefaultJobTitleOptions is seeded on first run — a starting point an Admin
// is expected to tune, not a fixed business rule (there was no prior
// hardcoded list to preserve, same framing as DefaultCompanySizeOptions).
var DefaultJobTitleOptions = []JobTitleOption{
	{Name: "Owner", IsActive: true},
	{Name: "CEO", IsActive: true},
	{Name: "Director", IsActive: true},
	{Name: "Manager", IsActive: true},
	{Name: "Staff", IsActive: true},
	{Name: "Other", IsActive: true},
}

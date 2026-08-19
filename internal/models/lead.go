package models

import "github.com/lib/pq"

type LeadStatus string

const (
	LeadStatusNew          LeadStatus = "New"
	LeadStatusContacted    LeadStatus = "Contacted"
	LeadStatusQualified    LeadStatus = "Qualified"
	LeadStatusDisqualified LeadStatus = "Disqualified"
)

// LeadSource is shared by Lead.source and Deal.channel (api-system-spec.md §3/§7.1).
type LeadSource string

const (
	LeadSourceReferral LeadSource = "Referral"
	LeadSourceWebsite  LeadSource = "Website"
	LeadSourceEvent    LeadSource = "Event"
	LeadSourceAds      LeadSource = "Ads"
	LeadSourceOther    LeadSource = "Other"
)

// Lead — api-system-spec.md §3. Embeds AuditedModel (not HardDeleteModel) so
// Delete/bulk-archive is recoverable via trash/restore instead of permanent.
type Lead struct {
	AuditedModel
	Name        string         `gorm:"not null" json:"name"`
	CompanyName string         `json:"company_name"`
	Email       string         `json:"email"`
	Phone       string         `json:"phone"`
	Source      LeadSource     `gorm:"type:varchar(64);index" json:"source"`
	Status      LeadStatus     `gorm:"type:varchar(16);default:'New';index" json:"status"`
	Notes       string         `json:"notes"`
	AssignedTo  *uint          `gorm:"index" json:"assigned_to"`
	Tags        pq.StringArray `gorm:"type:text[];index:idx_leads_tags,type:gin" json:"tags"`
	// ConvertedDealID is set once this Lead has been converted into a Deal
	// (nil = not yet converted). Prevents double-conversion.
	ConvertedDealID *uint `gorm:"index" json:"converted_deal_id"`
}

func (Lead) TableName() string { return "leads" }

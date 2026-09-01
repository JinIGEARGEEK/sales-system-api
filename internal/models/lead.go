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
	Name string `gorm:"not null" json:"name"`
	// CompanyID replaces the free-text CompanyName this Lead used to carry
	// (dropped 2026-08-24) — a real FK to Company, same as Deal/Contact,
	// instead of a bare string. Nullable: unlike Deal/Contact, a Lead can
	// still exist with no company picked yet (matching CompanyName's old
	// optional-ness — Create/Update never required it either). Existing
	// rows were backfilled from their old CompanyName text (exact
	// case-insensitive match against Companies, or a newly created Company
	// when no match existed) — see database.backfillLeadCompanyIDs.
	CompanyID  *uint          `gorm:"index" json:"company_id,omitempty"`
	Email      string         `json:"email"`
	Phone      string         `json:"phone"`
	Source     LeadSource     `gorm:"type:varchar(64);index" json:"source"`
	Status     LeadStatus     `gorm:"type:varchar(16);default:'New';index" json:"status"`
	Notes      string         `json:"notes"`
	AssignedTo *uint          `gorm:"index" json:"assigned_to"`
	Tags       pq.StringArray `gorm:"type:text[];index:idx_leads_tags,type:gin" json:"tags"`
	// ConvertedDealID is set once this Lead has been converted into a Deal
	// (nil = not yet converted). Prevents double-conversion.
	ConvertedDealID *uint `gorm:"index" json:"converted_deal_id"`
	// ProspectID is set when this Lead originated from a Marketing Prospect
	// via ProspectHandler.Convert (nil for a Lead created directly) — mirrors
	// Deal.LeadID's back-reference to its own originating record.
	ProspectID *uint `gorm:"index" json:"prospect_id,omitempty"`
	// Score/Classification — FR-CRM-006/007. Score is the sum of matching
	// active LeadScoringCriterion weights, recomputed on Create/Update.
	// Classification is derived from Score vs AppSettings.LeadScoringMqlThreshold
	// ("mql"), or set manually by a rep to "sql"; "none" below threshold.
	Score          int    `gorm:"not null;default:0" json:"score"`
	Classification string `gorm:"type:varchar(16);not null;default:'none';index" json:"classification"`
}

const (
	LeadClassificationNone LeadClassification = "none"
	LeadClassificationMQL  LeadClassification = "mql"
	LeadClassificationSQL  LeadClassification = "sql"
)

type LeadClassification string

func (Lead) TableName() string { return "leads" }

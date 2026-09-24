package models

import (
	"time"

	"github.com/lib/pq"
)

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
	// BusinessUnit/BusinessUnitItem mirror Deal's own fields (deal.go) — a
	// lightweight tag of which Project or Product this Lead is interested
	// in, not a real FK into the Project/CustomerProduct tables (api-system-
	// spec.md §7.1/§7.4 note this exact distinction for Deal; same applies
	// here). Carried over automatically to the Deal on conversion.
	BusinessUnit     *BusinessUnit `gorm:"type:varchar(16);index" json:"business_unit"`
	BusinessUnitItem *string       `json:"business_unit_item"`
	// ReferredByType/ReferredByID capture which existing Company or Contact
	// referred this Lead in (relevant when Source is "Referral", but not
	// enforced to only that source — a rep can still record it if the source
	// changes later). Both-or-neither: validated together in the handler
	// (models.IsValidReferrerType, activity.go), not via a DB constraint.
	// Typed as ActivityRelatedType itself (not a bare string) so it stays
	// compile-time-consistent with every other enum field on this struct
	// (Source/Status/BusinessUnit below) — restricted to just company/contact
	// of that broader enum's values, since Deal/Prospect aren't valid referrers.
	ReferredByType *ActivityRelatedType `gorm:"type:varchar(16)" json:"referred_by_type,omitempty"`
	ReferredByID   *uint                `gorm:"index" json:"referred_by_id,omitempty"`
	// Position orders this Lead's Kanban card within its own Status lane only —
	// see Deal.Position's doc comment (deal.go) for the full scheme; mirrored
	// here identically, driven by LeadHandler.UpdateStatus.
	Position float64 `gorm:"not null;default:0;index" json:"position"`
	// When this record entered its current lane; see stage_entered.go.
	StageEnteredAt *time.Time `gorm:"index" json:"stage_entered_at"`
	// The lane this record was in before its last move (see
	// MarkStageEntered); nil if it has never moved.
	PreviousStage *string `gorm:"type:varchar(64)" json:"previous_stage"`
}

const (
	LeadClassificationNone LeadClassification = "none"
	LeadClassificationMQL  LeadClassification = "mql"
	LeadClassificationSQL  LeadClassification = "sql"
)

type LeadClassification string

func (Lead) TableName() string { return "leads" }

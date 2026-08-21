package models

import "github.com/lib/pq"

type DealStage string

const (
	DealStageLead         DealStage = "Lead"
	DealStageQualified    DealStage = "Qualified"
	DealStageProposalSent DealStage = "Proposal Sent"
	DealStageNegotiation  DealStage = "Negotiation"
	DealStageWon          DealStage = "Won"
	DealStageLost         DealStage = "Lost"
)

type DealStatus string

const (
	DealStatusOpen DealStatus = "open"
	DealStatusWon  DealStatus = "won"
	DealStatusLost DealStatus = "lost"
)

type BusinessUnit string

const (
	BusinessUnitProject BusinessUnit = "Project"
	BusinessUnitProduct BusinessUnit = "Product"
)

// IsValidBusinessUnit reports whether bu is nil (BusinessUnit is optional) or
// one of the two fixed values above — this is a small, structural
// classification tied into business_unit_item's own behavior, not an
// open-ended categorization list, so it gets simple const-set validation
// like LostReason rather than an Admin-configurable option table.
func IsValidBusinessUnit(bu *BusinessUnit) bool {
	if bu == nil {
		return true
	}
	return *bu == BusinessUnitProject || *bu == BusinessUnitProduct
}

// LostReason is the required-when-Lost reason code for a Deal (Part of the
// probability/lost-reason forecast feature).
type LostReason string

const (
	LostReasonPrice      LostReason = "price"
	LostReasonTiming     LostReason = "timing"
	LostReasonCompetitor LostReason = "competitor"
	LostReasonNoBudget   LostReason = "no_budget"
	LostReasonOther      LostReason = "other"
)

// ValidLostReasons lists every accepted LostReason value, for handler-layer validation.
var ValidLostReasons = []LostReason{
	LostReasonPrice, LostReasonTiming, LostReasonCompetitor, LostReasonNoBudget, LostReasonOther,
}

func IsValidLostReason(r LostReason) bool {
	for _, v := range ValidLostReasons {
		if v == r {
			return true
		}
	}
	return false
}

// StageDefaultProbability returns the sensible default win-probability (0-100)
// for a given DealStage — used to prefill Deal.Probability when a caller
// doesn't supply one explicitly. Callers may always override it.
func StageDefaultProbability(stage DealStage) int {
	switch stage {
	case DealStageLead:
		return 10
	case DealStageQualified:
		return 30
	case DealStageProposalSent:
		return 50
	case DealStageNegotiation:
		return 75
	case DealStageWon:
		return 100
	case DealStageLost:
		return 0
	default:
		return 10
	}
}

// Deal — api-system-spec.md §7.1. Embeds AuditedModel (not HardDeleteModel) so
// Delete/bulk-archive is recoverable via trash/restore instead of permanent.
type Deal struct {
	AuditedModel
	CompanyID         uint           `gorm:"not null;index" json:"company_id"`
	ContactID         uint           `gorm:"not null;index" json:"contact_id"`
	Title             string         `gorm:"not null" json:"title"`
	Value             float64        `json:"value"`
	Stage             DealStage      `gorm:"type:varchar(64);default:'Lead';index" json:"stage"`
	Status            DealStatus     `gorm:"type:varchar(16);default:'open';index" json:"status"`
	ExpectedCloseDate *string        `json:"expected_close_date"`
	AssignedTo        *uint          `gorm:"index" json:"assigned_to"`
	Channel           LeadSource     `gorm:"type:varchar(64);index" json:"channel"`
	BusinessUnit      *BusinessUnit  `gorm:"type:varchar(16);index" json:"business_unit"`
	BusinessUnitItem  *string        `json:"business_unit_item"`
	Tags              pq.StringArray `gorm:"type:text[];index:idx_deals_tags,type:gin" json:"tags"`
	// LeadID traces this Deal back to the Lead it was converted from, if any.
	LeadID *uint `gorm:"index" json:"lead_id"`
	// Probability is the 0-100 win-probability, defaulted per-stage
	// (StageDefaultProbability) but always manually overridable.
	Probability *int `json:"probability"`
	// LostReason is required only when Stage/Status is set to Lost (validated
	// at the handler layer, see DealHandler.Update).
	LostReason *LostReason `gorm:"type:varchar(16)" json:"lost_reason"`
}

func (Deal) TableName() string { return "deals" }

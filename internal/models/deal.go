package models

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

// Deal — api-system-spec.md §7.1.
type Deal struct {
	HardDeleteModel
	CompanyID         uint          `gorm:"not null;index" json:"company_id"`
	ContactID         uint          `gorm:"not null;index" json:"contact_id"`
	Title             string        `gorm:"not null" json:"title"`
	Value             float64       `json:"value"`
	Stage             DealStage     `gorm:"type:varchar(32);default:'Lead';index" json:"stage"`
	Status            DealStatus    `gorm:"type:varchar(16);default:'open';index" json:"status"`
	ExpectedCloseDate *string       `json:"expected_close_date"`
	AssignedTo        *uint         `gorm:"index" json:"assigned_to"`
	Channel           LeadSource    `gorm:"type:varchar(16);index" json:"channel"`
	BusinessUnit      *BusinessUnit `gorm:"type:varchar(16);index" json:"business_unit"`
	BusinessUnitItem  *string       `json:"business_unit_item"`
	// LeadID traces this Deal back to the Lead it was converted from, if any.
	LeadID *uint `gorm:"index" json:"lead_id"`
}

func (Deal) TableName() string { return "deals" }

package models

import "github.com/lib/pq"

// ProspectStatus is a fixed enum, not admin-configurable (mirrors LeadStatus,
// not PipelineStage) — Marketing's funnel stage is a simple closed set.
type ProspectStatus string

const (
	ProspectStatusNew          ProspectStatus = "New"
	ProspectStatusEngaging     ProspectStatus = "Engaging"
	ProspectStatusNurturing    ProspectStatus = "Nurturing"
	ProspectStatusDisqualified ProspectStatus = "Disqualified"
	// ProspectStatusConverted is set automatically by Convert, never chosen
	// directly by a user (same spirit as Lead.ConvertedDealID gating re-conversion).
	ProspectStatusConverted ProspectStatus = "Converted"
)

// ProspectSource is its own type, deliberately NOT LeadSource — Marketing's
// funnel sources (Social Media, LINE OA, Email Campaign, ...) are a separate
// admin-configurable list (ProspectSourceOption, see pipeline_config.go) from
// Lead/Deal's (LeadSourceOption), added 2026-09-01 once Marketing's actual
// channel mix turned out not to overlap with Sales's lead-capture sources.
type ProspectSource string

// Prospect — the pre-Lead marketing funnel entity. Marketing works a Prospect
// (with an optional linked Company, same nullable-FK shape as Lead.CompanyID)
// before it's ready to hand off to Sales via Convert, which mirrors
// LeadHandler.Convert's resolve-or-create-Company/Contact-then-create-target
// pattern exactly, just one funnel stage earlier.
type Prospect struct {
	AuditedModel
	Name       string         `gorm:"not null" json:"name"`
	CompanyID  *uint          `gorm:"index" json:"company_id,omitempty"`
	Email      string         `json:"email"`
	Phone      string         `json:"phone"`
	Source     ProspectSource `gorm:"type:varchar(64);index" json:"source"`
	Status     ProspectStatus `gorm:"type:varchar(16);default:'New';index" json:"status"`
	Notes      string         `json:"notes"`
	AssignedTo *uint          `gorm:"index" json:"assigned_to"`
	Tags       pq.StringArray `gorm:"type:text[];index:idx_prospects_tags,type:gin" json:"tags"`
	// ConvertedLeadID is set once this Prospect has been converted into a Lead
	// (nil = not yet converted). Prevents double-conversion, same guard as
	// Lead.ConvertedDealID.
	ConvertedLeadID *uint `gorm:"index" json:"converted_lead_id"`
}

func (Prospect) TableName() string { return "prospects" }

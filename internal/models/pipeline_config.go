package models

// PipelineStage is an Admin-configurable pipeline stage — replaces the
// previously hardcoded DealStage enum as the source of truth for what stages
// exist, while DealStage itself stays as a plain string type (no Go-level
// enum to keep in sync) for backward compatibility with existing Deal rows.
//
// IsWonStage/IsLostStage drive status transitions (DealHandler.UpdateStage)
// and Kanban/badge coloring for custom stages that aren't the hardcoded
// "Won"/"Lost" names — a stage doesn't have to be named exactly "Won" to
// behave like one.
type PipelineStage struct {
	AuditedModel
	Name        string `gorm:"not null;uniqueIndex" json:"name"`
	SortOrder   int    `gorm:"not null;default:0;index" json:"sort_order"`
	IsActive    bool   `gorm:"not null;default:true;index" json:"is_active"`
	IsWonStage  bool   `gorm:"not null;default:false" json:"is_won_stage"`
	IsLostStage bool   `gorm:"not null;default:false" json:"is_lost_stage"`
}

func (PipelineStage) TableName() string { return "pipeline_stages" }

// LeadSourceOption is an Admin-configurable lead/deal acquisition source —
// replaces the hardcoded LeadSource enum as the source of truth for what
// sources exist (LeadSource itself stays a plain string type for backward
// compatibility, shared by Lead.source and Deal.channel).
type LeadSourceOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (LeadSourceOption) TableName() string { return "lead_source_options" }

// ProspectSourceOption is an Admin-configurable Prospect acquisition source
// — Marketing's own funnel-source list (added 2026-09-01), deliberately
// separate from LeadSourceOption above: Marketing's actual channels (Social
// Media, LINE OA, Email Campaign, Content/SEO, Cold Outreach, Marketing
// Campaign) don't overlap well with Sales's lead-capture sources
// (Referral/Website/Event/Ads/Other), so this is its own table rather than
// forcing both teams to share one list.
type ProspectSourceOption struct {
	AuditedModel
	Name     string `gorm:"not null;uniqueIndex" json:"name"`
	IsActive bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (ProspectSourceOption) TableName() string { return "prospect_source_options" }

// DefaultPipelineStages is the hardcoded stage list being retired — seeded
// verbatim (same order, same names) on first run so existing Deals validate
// unchanged. Kept here (not in database package) so handlers/seed code share
// one definition.
var DefaultPipelineStages = []PipelineStage{
	{Name: string(DealStageLead), SortOrder: 0, IsActive: true},
	{Name: string(DealStageQualified), SortOrder: 1, IsActive: true},
	{Name: string(DealStageProposalSent), SortOrder: 2, IsActive: true},
	{Name: string(DealStageNegotiation), SortOrder: 3, IsActive: true},
	{Name: string(DealStageWon), SortOrder: 4, IsActive: true, IsWonStage: true},
	{Name: string(DealStageLost), SortOrder: 5, IsActive: true, IsLostStage: true},
}

// DefaultLeadSourceOptions is the hardcoded LeadSource list being retired —
// seeded verbatim on first run so existing Leads/Deals validate unchanged.
var DefaultLeadSourceOptions = []LeadSourceOption{
	{Name: string(LeadSourceReferral), IsActive: true},
	{Name: string(LeadSourceWebsite), IsActive: true},
	{Name: string(LeadSourceEvent), IsActive: true},
	{Name: string(LeadSourceAds), IsActive: true},
	{Name: string(LeadSourceOther), IsActive: true},
}

// DefaultProspectSourceOptions is Marketing's own starter source list, seeded
// on first run same as DefaultLeadSourceOptions above (see
// cmd/api/main.go's seedPipelineConfig) — picked to cover the channels an
// inbound-marketing funnel actually uses that Sales's Lead/Deal source list
// doesn't (no "Referral"/"Event"/generic "Ads" duplication; LINE OA called
// out explicitly given this is a Thai company).
var DefaultProspectSourceOptions = []ProspectSourceOption{
	{Name: "Social Media", IsActive: true},
	{Name: "LINE OA", IsActive: true},
	{Name: "Email Campaign", IsActive: true},
	{Name: "Content/SEO", IsActive: true},
	{Name: "Cold Outreach", IsActive: true},
	{Name: "Marketing Campaign", IsActive: true},
}

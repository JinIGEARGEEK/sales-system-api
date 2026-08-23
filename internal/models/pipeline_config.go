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

// GetName/SetName/GetActive/SetActive satisfy handlers.OptionRow so
// LeadSourceHandler's CRUD lives in the shared generic handler.
func (o *LeadSourceOption) GetName() string  { return o.Name }
func (o *LeadSourceOption) SetName(n string) { o.Name = n }
func (o *LeadSourceOption) GetActive() bool  { return o.IsActive }
func (o *LeadSourceOption) SetActive(a bool) { o.IsActive = a }

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

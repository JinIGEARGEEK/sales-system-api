package models

// LeadScoringCriterion is an Admin-configurable weighted rule used to compute
// a Lead's Score (FR-CRM-006). Field/MatchValue are matched against the Lead
// row being scored ("source" -> Lead.Source, "has_company_name" -> non-empty
// Lead.CompanyName) — kept as plain strings rather than a fixed enum so new
// match fields can be added without a schema change, same trade-off
// PipelineStage/LeadSourceOption make for their own config values.
type LeadScoringCriterion struct {
	AuditedModel
	Name       string `gorm:"not null;uniqueIndex" json:"name"`
	Field      string `gorm:"not null" json:"field"`
	MatchValue string `json:"match_value"`
	Weight     int    `gorm:"not null;default:0" json:"weight"`
	IsActive   bool   `gorm:"not null;default:true;index" json:"is_active"`
}

func (LeadScoringCriterion) TableName() string { return "lead_scoring_criteria" }

// DefaultLeadScoringCriteria is seeded on first run — a starting point an
// Admin is expected to tune, not a fixed business rule.
var DefaultLeadScoringCriteria = []LeadScoringCriterion{
	{Name: "Referral source", Field: "source", MatchValue: string(LeadSourceReferral), Weight: 30, IsActive: true},
	{Name: "Website source", Field: "source", MatchValue: string(LeadSourceWebsite), Weight: 15, IsActive: true},
	{Name: "Event source", Field: "source", MatchValue: string(LeadSourceEvent), Weight: 20, IsActive: true},
	{Name: "Has company name", Field: "has_company_name", MatchValue: "", Weight: 15, IsActive: true},
	{Name: "Has phone number", Field: "has_phone", MatchValue: "", Weight: 10, IsActive: true},
}

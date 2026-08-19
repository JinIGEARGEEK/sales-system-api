package models

import "github.com/lib/pq"

type ActiveArchivedStatus string

const (
	StatusActive   ActiveArchivedStatus = "active"
	StatusArchived ActiveArchivedStatus = "archived"
)

// Company — api-system-spec.md §4. LegalName/Address/TaxID are used on Contract
// PDF exports — a real legal document needs the registered party details.
type Company struct {
	AuditedModel
	Name      string               `gorm:"not null" json:"name"`
	Industry  string               `gorm:"index" json:"industry"`
	Size      string               `json:"size"`
	Website   string               `json:"website"`
	Tags      pq.StringArray       `gorm:"type:text[];index:idx_companies_tags,type:gin" json:"tags"`
	Notes     string               `json:"notes"`
	Status    ActiveArchivedStatus `gorm:"type:varchar(16);default:'active';index" json:"status"`
	LegalName *string              `json:"legal_name"`
	Address   *string              `json:"address"`
	TaxID     *string              `json:"tax_id"`
	// Domain is the lowercase, scheme/www-stripped host extracted from Website
	// at write time (see utils.ExtractDomain) — indexed so ImportCompanies'
	// domain-based dedupe is an indexed lookup instead of an in-memory scan of
	// every company with a website. Empty when Website has no discernible host.
	Domain string `gorm:"index" json:"-"`
}

func (Company) TableName() string { return "companies" }

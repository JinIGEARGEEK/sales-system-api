package models

import "github.com/lib/pq"

type ActiveArchivedStatus string

const (
	StatusActive   ActiveArchivedStatus = "active"
	StatusArchived ActiveArchivedStatus = "archived"
)

// Company — api-system-spec.md §4. LegalName/Address/TaxID are used on Contract
// PDF exports — a real legal document needs the registered party details.
// TaxID + BranchCode is how an accounting integration identifies a Company
// (a Thai full tax invoice names the buyer's branch), hence TaxID's index
// for the exact-match ?tax_id= filter.
type Company struct {
	AuditedModel
	Name        string               `gorm:"not null" json:"name"`
	Industry    string               `gorm:"index" json:"industry"`
	Size        string               `json:"size"`
	RevenueSize string               `json:"revenue_size"`
	Website     string               `json:"website"`
	Tags        pq.StringArray       `gorm:"type:text[];index:idx_companies_tags,type:gin" json:"tags"`
	Notes       string               `json:"notes"`
	Status      ActiveArchivedStatus `gorm:"type:varchar(16);default:'active';index" json:"status"`
	LegalName   *string              `json:"legal_name"`
	Address     *string              `json:"address"`
	TaxID       *string              `gorm:"index" json:"tax_id"`
	// BranchCode/PostalCode are each exactly five digits when set
	// (BranchCode "00000" = head office). Newer than every existing client,
	// so Update leaves them unchanged when the body omits them — see
	// CompanyHandler.Update.
	BranchCode *string `gorm:"type:varchar(5)" json:"branch_code"`
	PostalCode *string `gorm:"type:varchar(5)" json:"postal_code"`
	// Domain is the lowercase, scheme/www-stripped host extracted from Website
	// at write time (see utils.ExtractDomain) — indexed so ImportCompanies'
	// domain-based dedupe is an indexed lookup instead of an in-memory scan of
	// every company with a website. Empty when Website has no discernible host.
	Domain string `gorm:"index" json:"-"`
}

func (Company) TableName() string { return "companies" }

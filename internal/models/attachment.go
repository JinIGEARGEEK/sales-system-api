package models

// AttachmentCategory tags what kind of working document this is — quotation,
// proposal, estimation, plan, or general supporting material.
type AttachmentCategory string

const (
	AttachmentCategoryQuotation  AttachmentCategory = "Quotation"
	AttachmentCategoryProposal   AttachmentCategory = "Proposal"
	AttachmentCategoryEstimation AttachmentCategory = "Estimation"
	AttachmentCategoryPlan       AttachmentCategory = "Plan"
	AttachmentCategorySupport    AttachmentCategory = "Support"
	AttachmentCategoryOther      AttachmentCategory = "Other"
)

// AttachmentRelatedType is deliberately broader than ActivityRelatedType (which
// excludes Lead) — attachments are useful before a Lead ever converts to a Deal.
type AttachmentRelatedType string

const (
	AttachmentRelatedLead    AttachmentRelatedType = "lead"
	AttachmentRelatedDeal    AttachmentRelatedType = "deal"
	AttachmentRelatedCompany AttachmentRelatedType = "company"
	AttachmentRelatedProject AttachmentRelatedType = "project"
	// AttachmentRelatedQuote backs the Quote editor's attachments section
	// (quotation-builder rebuild) — reuses this same generic model/handler
	// rather than a Quote-specific attachments table.
	AttachmentRelatedQuote AttachmentRelatedType = "quote"
)

// Attachment — api-system-spec.md §8.6. Exactly one of FileURL/ExternalURL is
// set: FileURL for an uploaded binary (PDF/image/spreadsheet, via the shared
// SaveUpload flow), ExternalURL for a linked doc (Google Sheets/Docs/Drive)
// that isn't downloaded/re-hosted.
type Attachment struct {
	HardDeleteModel
	// Composite index on (related_type, related_id) — every List query filters
	// on both together (see AttachmentHandler.List), same reasoning as Activity's.
	RelatedType  AttachmentRelatedType `gorm:"type:varchar(16);index:idx_attachments_related,priority:1" json:"related_type"`
	RelatedID    uint                  `gorm:"index:idx_attachments_related,priority:2" json:"related_id"`
	Category     AttachmentCategory    `gorm:"type:varchar(16)" json:"category"`
	FileName     string                `json:"file_name"`
	FileURL      *string               `json:"file_url"`
	ExternalURL  *string               `json:"external_url"`
	FileSize     *int64                `json:"file_size"`
	MimeType     *string               `json:"mime_type"`
	UploadedByID uint                  `json:"uploaded_by"`
}

func (Attachment) TableName() string { return "attachments" }

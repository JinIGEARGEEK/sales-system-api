package models

// CampaignType — currently just win_back, structured (type + const block +
// Valid*/IsValid* pair) the same way TaskPriority is, so more campaign types
// can be added later without changing the validation shape.
type CampaignType string

const (
	CampaignTypeWinBack CampaignType = "win_back"
)

// ValidCampaignTypes lists every accepted CampaignType value, for handler-layer validation.
var ValidCampaignTypes = []CampaignType{CampaignTypeWinBack}

func IsValidCampaignType(t CampaignType) bool {
	for _, v := range ValidCampaignTypes {
		if v == t {
			return true
		}
	}
	return false
}

// Campaign — a named, typed batch of outreach (e.g. a dormant-company
// win-back push) that Tasks can be tagged against via Task.CampaignID, so
// progress on the batch can be tracked as a whole (see CampaignHandler.Progress).
type Campaign struct {
	HardDeleteModel
	Name      string       `gorm:"not null" json:"name"`
	Type      CampaignType `gorm:"type:varchar(16)" json:"type"`
	CreatedBy *uint        `json:"created_by"`
}

func (Campaign) TableName() string { return "campaigns" }

package models

import "time"

// OpenAPIRequestLog is an append-only record of every write (any non-GET
// call — POST/PUT/PATCH) an /open/* API key makes. This exists alongside
// AuditLogEntry, not instead of it: AuditLogEntry's actor is
// CreatedBy/UpdatedBy — the key's OWNER user —
// and one owner can have several keys (e.g. one per internal system synced
// against this CRM), so nothing in a Company/Contact row itself can answer
// "which integration made this change". With multiple internal systems now
// reading/writing Company/Contact data here and this CRM meant to be the
// source of truth for it, that provenance question needs a real answer, not
// just "some call attributed to Jane sometime this month".
type OpenAPIRequestLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	APIKeyID     uint      `gorm:"not null;index" json:"api_key_id"`
	OwnerUserID  uint      `gorm:"not null" json:"owner_user_id"`
	Method       string    `gorm:"not null" json:"method"`
	Path         string    `gorm:"not null" json:"path"`
	ResourceType string    `json:"resource_type"`
	ResourceID   uint      `json:"resource_id"`
	StatusCode   int       `gorm:"not null" json:"status_code"`
	CreatedAt    time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (OpenAPIRequestLog) TableName() string { return "open_api_request_logs" }

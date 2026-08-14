package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// JSONMap stores an arbitrary before/after snapshot as jsonb.
type JSONMap map[string]interface{}

func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func (m *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*m = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		s, ok := value.(string)
		if !ok {
			*m = nil
			return nil
		}
		bytes = []byte(s)
	}
	return json.Unmarshal(bytes, m)
}

// AuditLogEntry — api-system-spec.md §8.5. 🔜 Planned. Append-only: no
// update/delete route should exist for this resource at all (NFR-007).
type AuditLogEntry struct {
	ID         uint    `gorm:"primaryKey" json:"id"`
	EntityType string  `gorm:"type:varchar(32);index" json:"entity_type"`
	EntityID   uint    `gorm:"index" json:"entity_id"`
	Action     string  `gorm:"type:varchar(64)" json:"action"`
	Before     JSONMap `gorm:"type:jsonb" json:"before"`
	After      JSONMap `gorm:"type:jsonb" json:"after"`
	ActorID    uint      `json:"actor_id"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (AuditLogEntry) TableName() string { return "audit_log_entries" }

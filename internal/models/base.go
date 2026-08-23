package models

import (
	"time"

	"gorm.io/gorm"
)

// AuditedModel is the base every resource embeds per api-system-spec.md §1.6:
// soft-delete (DeletedAt) + created_by/updated_by/deleted_by actor tracking.
type AuditedModel struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
	CreatedBy *uint          `json:"created_by,omitempty"`
	UpdatedBy *uint          `json:"updated_by,omitempty"`
	DeletedBy *uint          `json:"deleted_by,omitempty"`
}

// SetCreatedBy/SetUpdatedBy/SetDeletedBy let generic code (handlers.OptionHandler)
// stamp actor-tracking fields on any AuditedModel-embedding resource without a
// type switch over every concrete type.
func (m *AuditedModel) SetCreatedBy(id *uint) { m.CreatedBy = id }
func (m *AuditedModel) SetUpdatedBy(id *uint) { m.UpdatedBy = id }
func (m *AuditedModel) SetDeletedBy(id *uint) { m.DeletedBy = id }

// SimpleModel is for resources whose spec shape only has created_at (no soft-delete columns).
type SimpleModel struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// HardDeleteModel is for resources the spec deletes with a plain DELETE (no soft-delete
// wording in api-system-spec.md, unlike Company/Contact/Tag/User which explicitly say
// "soft-delete"/"archived") — Lead, Deal, Activity, Payment, Task, Quote, Contract.
type HardDeleteModel struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

package utils

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// IsActivePipelineStage reports whether name matches an active PipelineStage
// row — the DB-backed replacement for the old hardcoded DealStage whitelist.
// Empty name is allowed through (unset stage falls back to its model default).
func IsActivePipelineStage(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.PipelineStage{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActiveLeadSource reports whether name matches an active LeadSourceOption
// row — the DB-backed replacement for the old hardcoded LeadSource whitelist.
// Empty name is allowed through (channel/source is optional on Deal/Lead).
func IsActiveLeadSource(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.LeadSourceOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsWonStage and IsLostStage report whether stage should be treated as the
// Won/Lost terminal state — preferring the configured PipelineStage row's
// IsWonStage/IsLostStage flag (so a custom, admin-renamed stage still behaves
// like Won/Lost), and falling back to the hardcoded name match if no
// PipelineStage row exists yet (e.g. right after a migration, before the seed
// runs). Mirrors the resolution DealHandler.UpdateStage already used, now
// shared so Create/Update's lost_reason validation stays in sync with it.
func IsWonStage(db *gorm.DB, stage models.DealStage) bool {
	var row models.PipelineStage
	hasRow := db.Where("name = ?", stage).First(&row).Error == nil
	return (hasRow && row.IsWonStage) || (!hasRow && stage == models.DealStageWon)
}

func IsLostStage(db *gorm.DB, stage models.DealStage) bool {
	var row models.PipelineStage
	hasRow := db.Where("name = ?", stage).First(&row).Error == nil
	return (hasRow && row.IsLostStage) || (!hasRow && stage == models.DealStageLost)
}

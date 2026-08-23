package utils

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// isActiveOption is the shared body behind every IsActiveX below — each one
// is a DB-backed replacement for what used to be a hardcoded/frontend-only
// whitelist, and all of them boil down to "does an active row with this name
// exist in T's table". Empty name is always allowed through: the field each
// one guards is optional, so an unset value shouldn't fail validation.
func isActiveOption[T any](db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(new(T)).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActivePipelineStage reports whether name matches an active PipelineStage row.
func IsActivePipelineStage(db *gorm.DB, name string) bool {
	return isActiveOption[models.PipelineStage](db, name)
}

// IsActiveLeadSource reports whether name matches an active LeadSourceOption row.
func IsActiveLeadSource(db *gorm.DB, name string) bool {
	return isActiveOption[models.LeadSourceOption](db, name)
}

// IsActiveIndustry reports whether name matches an active IndustryOption row.
func IsActiveIndustry(db *gorm.DB, name string) bool {
	return isActiveOption[models.IndustryOption](db, name)
}

// IsActiveCompanySize reports whether name matches an active CompanySizeOption row.
func IsActiveCompanySize(db *gorm.DB, name string) bool {
	return isActiveOption[models.CompanySizeOption](db, name)
}

// IsActiveRevenueSize reports whether name matches an active RevenueSizeOption row.
func IsActiveRevenueSize(db *gorm.DB, name string) bool {
	return isActiveOption[models.RevenueSizeOption](db, name)
}

// IsActiveJobTitle reports whether name matches an active JobTitleOption row.
func IsActiveJobTitle(db *gorm.DB, name string) bool {
	return isActiveOption[models.JobTitleOption](db, name)
}

// IsActiveProductCategory reports whether name matches an active ProductCategoryOption row.
func IsActiveProductCategory(db *gorm.DB, name string) bool {
	return isActiveOption[models.ProductCategoryOption](db, name)
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

// StageDefaultProbability resolves the win-probability default (0-100) for a
// stage the same way IsWonStage/IsLostStage resolve their terminal-state
// flags: prefer the configured PipelineStage row over the hardcoded stage
// name, so a custom Admin-added stage — or a hardcoded stage the Admin
// renamed away from "Won"/"Lost" while keeping its flag — still gets a
// sensible value instead of models.StageDefaultProbability's flat 10 for
// anything it doesn't recognize.
//
// Resolution order:
//  1. No PipelineStage row for this name at all (e.g. pre-seed) — fall back
//     to the hardcoded models.StageDefaultProbability(stage) switch, same
//     fallback IsWonStage/IsLostStage use.
//  2. Row found and flagged Won/Lost — 100/0, regardless of the row's name.
//  3. Row found, in-between — interpolate 10-90 across the row's sort_order
//     position among all active non-Won/non-Lost stages, earliest stage
//     getting ~10 and the latest getting ~90, single-stage funnels landing at 10.
func StageDefaultProbability(db *gorm.DB, stage models.DealStage) int {
	var row models.PipelineStage
	if err := db.Where("name = ?", stage).First(&row).Error; err != nil {
		return models.StageDefaultProbability(stage)
	}
	switch {
	case row.IsWonStage:
		return 100
	case row.IsLostStage:
		return 0
	}

	var funnel []models.PipelineStage
	db.Where("is_active = ? AND is_won_stage = ? AND is_lost_stage = ?", true, false, false).
		Order("sort_order ASC, id ASC").Find(&funnel)

	if len(funnel) <= 1 {
		return 10
	}
	position := -1
	for i, s := range funnel {
		if s.ID == row.ID {
			position = i
			break
		}
	}
	if position < 0 {
		// Stage is inactive or otherwise excluded from the funnel query above
		// (shouldn't normally happen for a stage a Deal is being set to) —
		// fall back to the hardcoded default rather than guessing a position.
		return models.StageDefaultProbability(stage)
	}
	const minProb, maxProb = 10, 90
	return minProb + (position*(maxProb-minProb))/(len(funnel)-1)
}

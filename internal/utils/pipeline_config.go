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

// IsActiveIndustry reports whether name matches an active IndustryOption
// row — the DB-backed replacement for the old frontend-only INDUSTRY_OPTIONS
// whitelist. Empty name is allowed through (Industry has no NOT NULL
// constraint at the DB level).
func IsActiveIndustry(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.IndustryOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActiveCompanySize reports whether name matches an active
// CompanySizeOption row. Empty name is allowed through — Size is optional.
func IsActiveCompanySize(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.CompanySizeOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActiveRevenueSize reports whether name matches an active
// RevenueSizeOption row. Empty name is allowed through — RevenueSize is optional.
func IsActiveRevenueSize(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.RevenueSizeOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActiveJobTitle reports whether name matches an active JobTitleOption
// row. Empty name is allowed through — Contact.RoleTitle is optional.
func IsActiveJobTitle(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.JobTitleOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActiveProductCategory reports whether name matches an active
// ProductCategoryOption row. Empty name is allowed through — Category is
// optional.
func IsActiveProductCategory(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.ProductCategoryOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
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

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

// IsActiveProspectSource reports whether name matches an active
// ProspectSourceOption row — Marketing's own funnel-source list, separate
// from IsActiveLeadSource's LeadSourceOption table. Empty name is allowed
// through, same as every other optional-field option check here.
func IsActiveProspectSource(db *gorm.DB, name string) bool {
	if name == "" {
		return true
	}
	var count int64
	db.Model(&models.ProspectSourceOption{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// IsActiveProspectStage reports whether name matches an active ProspectStage
// row — the DB-backed replacement for the old hardcoded ProspectStatus
// working-stage whitelist. Empty name is allowed through, same as the other
// option checks here, and "Converted" is always allowed through too since
// it's a system-set terminal status (see ProspectStage's own doc) that never
// gets a row in this table.
func IsActiveProspectStage(db *gorm.DB, name string) bool {
	if name == "" || name == string(models.ProspectStatusConverted) {
		return true
	}
	var count int64
	db.Model(&models.ProspectStage{}).Where("name = ? AND is_active = ?", name, true).Count(&count)
	return count > 0
}

// EnsureActiveIndustry finds-or-creates an active IndustryOption matching
// name, reactivating it if it was previously soft-deactivated by an Admin.
// Runs on Company Create/Update now that the frontend Industry field is a
// free-typed combobox: any value a user types becomes a real, reusable
// industry option rather than being bounced with a validation error. Empty
// name is a no-op — Industry has no NOT NULL constraint at the DB level.
func EnsureActiveIndustry(db *gorm.DB, name string) error {
	if name == "" {
		return nil
	}
	opt, err := findIndustryOption(db, name)
	if err != nil {
		return err
	}
	if opt != nil {
		if !opt.IsActive {
			opt.IsActive = true
			return db.Save(opt).Error
		}
		return nil
	}
	// name.uniqueIndex means two concurrent Creates of a brand-new industry
	// can both miss the lookup above and race here — the loser's Create
	// fails on the unique constraint, not because anything is actually
	// wrong. Re-resolve by name rather than surfacing that as a 500: the
	// winner's row is exactly the one this call wanted to ensure exists.
	if err := db.Create(&models.IndustryOption{Name: name, IsActive: true}).Error; err != nil {
		if reOpt, reErr := findIndustryOption(db, name); reErr == nil && reOpt != nil {
			return nil
		}
		return err
	}
	return nil
}

func findIndustryOption(db *gorm.DB, name string) (*models.IndustryOption, error) {
	var opt models.IndustryOption
	err := db.Where("name = ?", name).First(&opt).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &opt, nil
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

package utils

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// LogCompanyActivity records a company-scoped Activity as the side effect of
// a Prospect/Lead/Deal stage or status change, so Company.last_activity_at
// (computed as MAX(activities.created_at) WHERE related_type = 'company')
// reflects that the customer was actually contacted. No-ops when companyID
// is 0 — a record with no linked Company has nothing to bump. Call it inside
// the same transaction as the triggering save so the two stay atomic.
func LogCompanyActivity(tx *gorm.DB, companyID uint, subject string, actorID uint) error {
	if companyID == 0 {
		return nil
	}
	return tx.Create(&models.Activity{
		Type:        models.ActivityTypeNote,
		Subject:     subject,
		RelatedType: models.RelatedTypeCompany,
		RelatedID:   companyID,
		CreatedByID: actorID,
	}).Error
}

// LogStatusChangeActivity is LogCompanyActivity's entry point for
// Prospect/Lead status transitions (ProspectHandler.Update, LeadHandler.Update):
// no-ops when oldStatus == newStatus (the form resubmits the full record on
// every save, so this is the only reliable "did it actually change" check —
// mirrors Deal's own oldStage != deal.Stage gate). companyID is resolved to
// the record's current (post-mutation) value, falling back to its
// pre-mutation one — covers the same-save case where a Company was just
// linked alongside the status change. Both are nullable since Prospect/Lead
// may have no linked Company at all, in which case this no-ops too.
func LogStatusChangeActivity[S ~string](tx *gorm.DB, entityLabel string, oldStatus, newStatus S, companyID, fallbackCompanyID *uint, actorID uint) error {
	if oldStatus == newStatus {
		return nil
	}
	resolved := companyID
	if resolved == nil {
		resolved = fallbackCompanyID
	}
	if resolved == nil {
		return nil
	}
	subject := fmt.Sprintf("%s status changed: %s → %s", entityLabel, oldStatus, newStatus)
	return LogCompanyActivity(tx, *resolved, subject, actorID)
}

package handlers

import (
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

// merge_refs.go — the re-pointing half of a Company/Contact merge (merge.go).
//
// Every reference to a Company:
//   - contacts.company_id                      (moved first — see merge.go's lock order)
//   - deals.company_id
//   - leads.company_id
//   - prospects.company_id
//   - projects.company_id
//   - customer_products.company_id
//   - activities   related_type 'company'      (company timeline, last_activity_at)
//   - attachments  related_type 'company'
//   - tasks        related_type 'company'      (incl. Campaign target Tasks)
//   - leads.referred_by_type/_id 'company'     (moved key "lead_referrals")
//   - notification_logs of 'company' rules     (dormant-company alerts; entity_id is the Company)
//
// Every reference to a Contact:
//   - deals.contact_id
//   - activities   related_type 'contact'
//   - tasks        related_type 'contact'      (incl. Campaign target Tasks)
//   - leads.referred_by_type/_id 'contact'     (moved key "lead_referrals")
//
// Deliberately not moved: audit_log_entries (a source's history stays on it;
// its merged_into row points at the target), open_api_request_logs and
// idempotency_keys (request history / stored responses, not live links).
// Quotes, contracts, payments and installments hang off a Deal, not a
// Company/Contact, so they follow their Deal. Tag rows are only names.
//
// Soft-deleted referencing rows are moved too (raw UPDATEs, no default
// scope), so something restored from Trash later points at the surviving
// record. Rows that have updated_at get it bumped, so updated_since syncs see
// the change.

// mergeRef is one UPDATE that re-points a reference column.
type mergeRef struct {
	key        string // moved-count key in the response
	table      string
	column     string
	typeColumn string // polymorphic type column, "" for a plain FK
	typeValue  string
}

func (r mergeRef) apply(tx *gorm.DB, targetID uint, sourceIDs []uint) (int64, error) {
	sql := "UPDATE " + r.table + " SET " + r.column + " = ?, updated_at = NOW() WHERE " + r.column + " IN ?"
	args := []interface{}{targetID, sourceIDs}
	if r.typeColumn != "" {
		sql += " AND " + r.typeColumn + " = ?"
		args = append(args, r.typeValue)
	}
	res := tx.Exec(sql, args...)
	return res.RowsAffected, res.Error
}

var companyMergeRefs = []mergeRef{
	{key: "contacts", table: "contacts", column: "company_id"},
	{key: "deals", table: "deals", column: "company_id"},
	{key: "leads", table: "leads", column: "company_id"},
	{key: "prospects", table: "prospects", column: "company_id"},
	{key: "projects", table: "projects", column: "company_id"},
	{key: "customer_products", table: "customer_products", column: "company_id"},
	{key: "activities", table: "activities", column: "related_id", typeColumn: "related_type", typeValue: string(models.RelatedTypeCompany)},
	{key: "attachments", table: "attachments", column: "related_id", typeColumn: "related_type", typeValue: string(models.AttachmentRelatedCompany)},
	{key: "tasks", table: "tasks", column: "related_id", typeColumn: "related_type", typeValue: string(models.RelatedTypeCompany)},
	{key: "lead_referrals", table: "leads", column: "referred_by_id", typeColumn: "referred_by_type", typeValue: string(models.RelatedTypeCompany)},
}

var contactMergeRefs = []mergeRef{
	{key: "deals", table: "deals", column: "contact_id"},
	{key: "activities", table: "activities", column: "related_id", typeColumn: "related_type", typeValue: string(models.RelatedTypeContact)},
	{key: "tasks", table: "tasks", column: "related_id", typeColumn: "related_type", typeValue: string(models.RelatedTypeContact)},
	{key: "lead_referrals", table: "leads", column: "referred_by_id", typeColumn: "referred_by_type", typeValue: string(models.RelatedTypeContact)},
}

func applyMergeRefs(tx *gorm.DB, refs []mergeRef, targetID uint, sourceIDs []uint, moved map[string]int64) error {
	for _, r := range refs {
		n, err := r.apply(tx, targetID, sourceIDs)
		if err != nil {
			return err
		}
		moved[r.key] += n
		moved["total"] += n
	}
	return nil
}

// repointCompanyRefs moves every Company reference (see the list above) and
// returns the per-table counts plus "total".
func repointCompanyRefs(tx *gorm.DB, targetID uint, sourceIDs []uint) (map[string]int64, error) {
	moved := map[string]int64{"notification_logs": 0, "total": 0}
	if err := settleMergedPrimaryContacts(tx, targetID, sourceIDs); err != nil {
		return nil, err
	}
	if err := applyMergeRefs(tx, companyMergeRefs, targetID, sourceIDs, moved); err != nil {
		return nil, err
	}
	n, err := repointCompanyNotificationLogs(tx, targetID, sourceIDs)
	if err != nil {
		return nil, err
	}
	moved["notification_logs"] = n
	moved["total"] += n
	return moved, nil
}

// repointContactRefs moves every Contact reference and returns the counts.
func repointContactRefs(tx *gorm.DB, targetID uint, sourceIDs []uint) (map[string]int64, error) {
	moved := map[string]int64{"total": 0}
	if err := applyMergeRefs(tx, contactMergeRefs, targetID, sourceIDs, moved); err != nil {
		return nil, err
	}
	return moved, nil
}

// settleMergedPrimaryContacts keeps at most one Primary Contact on the target
// once the sources' Contacts move over: the target's own Primary if it has
// one, else the first source (source_ids order) that has one. Runs before
// the move, so it only touches the sources' Contacts.
func settleMergedPrimaryContacts(tx *gorm.DB, targetID uint, sourceIDs []uint) error {
	var targetPrimaries int64
	if err := tx.Model(&models.Contact{}).
		Where("company_id = ? AND is_primary = ?", targetID, true).
		Count(&targetPrimaries).Error; err != nil {
		return err
	}
	var keep uint
	if targetPrimaries == 0 {
		var primaries []models.Contact
		if err := tx.Select("id", "company_id").
			Where("company_id IN ? AND is_primary = ?", sourceIDs, true).
			Order("id").Find(&primaries).Error; err != nil {
			return err
		}
		firstBySource := map[uint]uint{}
		for _, p := range primaries {
			if _, ok := firstBySource[p.CompanyID]; !ok {
				firstBySource[p.CompanyID] = p.ID
			}
		}
		for _, sid := range sourceIDs {
			if id, ok := firstBySource[sid]; ok {
				keep = id
				break
			}
		}
	}
	return tx.Exec("UPDATE contacts SET is_primary = false WHERE company_id IN ? AND is_primary AND id <> ?",
		sourceIDs, keep).Error
}

// repointCompanyNotificationLogs moves the sources' dormant-company alert
// logs (rules with entity_type 'company') to the target, so the target isn't
// re-alerted for a tier a source was already alerted for. A log whose
// (rule, context) the target already has stays on the source, since the
// unique (rule_id, entity_id, context) index allows only one.
func repointCompanyNotificationLogs(tx *gorm.DB, targetID uint, sourceIDs []uint) (int64, error) {
	res := tx.Exec(`
		UPDATE notification_logs nl SET entity_id = ?
		WHERE nl.id IN (
			SELECT DISTINCT ON (l.rule_id, l.context) l.id
			FROM notification_logs l
			JOIN notification_rules r ON r.id = l.rule_id AND r.entity_type = ?
			WHERE l.entity_id IN ?
			  AND NOT EXISTS (SELECT 1 FROM notification_logs t
			                  WHERE t.rule_id = l.rule_id AND t.context = l.context AND t.entity_id = ?)
			ORDER BY l.rule_id, l.context, l.notified_at, l.id
		)`, targetID, models.NotificationEntityCompany, sourceIDs, targetID)
	return res.RowsAffected, res.Error
}

package handlers

import (
	"errors"
	"slices"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// openRecordCounts is how many open pipeline records a user owns (or, after
// a reassign, how many were moved): Deals with status open, Leads and
// Prospects not yet converted or disqualified, and pending Tasks.
type openRecordCounts struct {
	Deals     int64 `json:"deals"`
	Leads     int64 `json:"leads"`
	Prospects int64 `json:"prospects"`
	Tasks     int64 `json:"tasks"`
	Total     int64 `json:"total"`
}

// openOwnedRecords lists, per owning table, the "still open" filter that
// openRecordCounts counts and reassignOpenRecords moves. auditedModel marks
// the tables with an updated_by column (Task is a HardDeleteModel).
// auditEntity, when set, makes reassignOpenRecords also write one
// "reassigned" audit row per moved record, the shape the entity's own
// single-record reassign writes. Only Deals have one (PATCH
// /deals/:id/reassign, PUT /deals/:id). Leads, Prospects and Tasks have no
// per-record reassign audit outside their bulk endpoints, so the summary
// records_reassigned row covers them.
var openOwnedRecords = []struct {
	model        interface{}
	auditedModel bool
	auditEntity  string
	open         func(*gorm.DB) *gorm.DB
	count        func(*openRecordCounts) *int64
}{
	{&models.Deal{}, true, "deal", func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ?", models.DealStatusOpen)
	}, func(o *openRecordCounts) *int64 { return &o.Deals }},
	{&models.Lead{}, true, "", func(q *gorm.DB) *gorm.DB {
		return q.Where("converted_deal_id IS NULL AND status <> ?", models.LeadStatusDisqualified)
	}, func(o *openRecordCounts) *int64 { return &o.Leads }},
	{&models.Prospect{}, true, "", func(q *gorm.DB) *gorm.DB {
		return q.Where("converted_lead_id IS NULL AND status NOT IN ?",
			[]models.ProspectStatus{models.ProspectStatusDisqualified, models.ProspectStatusConverted})
	}, func(o *openRecordCounts) *int64 { return &o.Prospects }},
	{&models.Task{}, false, "", func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ?", models.TaskStatusPending)
	}, func(o *openRecordCounts) *int64 { return &o.Tasks }},
}

// reassignAuditBatchSize caps rows per audit INSERT (7 bind params each, well
// under Postgres's 65535 limit), so any realistic hand-off is one INSERT.
const reassignAuditBatchSize = 1000

// countOpenRecords counts the open records userID still owns, so a
// deactivate/delete response can tell the UI to offer a reassign.
func countOpenRecords(db *gorm.DB, userID uint) (openRecordCounts, error) {
	var out openRecordCounts
	for _, r := range openOwnedRecords {
		q := r.open(db.Model(r.model).Where("assigned_to = ?", userID))
		if err := q.Count(r.count(&out)).Error; err != nil {
			return out, err
		}
		out.Total += *r.count(&out)
	}
	return out, nil
}

// reassignOpenRecords moves every open record owned by from to to inside tx,
// writing one records_reassigned audit row on the user with the counts
// (none when nothing moved), plus a per-record "reassigned" row for each
// moved record of a table with an auditEntity, all in one batched INSERT.
// Closed records keep their owner as history.
func reassignOpenRecords(tx *gorm.DB, from, to, actorID uint) (openRecordCounts, error) {
	var out openRecordCounts
	var perRecord []models.AuditLogEntry
	for _, r := range openOwnedRecords {
		set := map[string]interface{}{"assigned_to": to}
		if r.auditedModel {
			set["updated_by"] = actorID
		}
		q := r.open(tx.Model(r.model).Where("assigned_to = ?", from))
		if r.auditEntity != "" {
			// Lock and list the rows first so the audit rows name exactly
			// the records the UPDATE moves.
			var ids []uint
			if err := q.Clauses(clause.Locking{Strength: "UPDATE"}).Pluck("id", &ids).Error; err != nil {
				return out, err
			}
			if len(ids) == 0 {
				continue
			}
			q = tx.Model(r.model).Where("id IN ?", ids)
			for _, id := range ids {
				perRecord = append(perRecord, models.AuditLogEntry{
					EntityType: r.auditEntity, EntityID: id, Action: "reassigned",
					Before:  models.JSONMap{"assigned_to": from},
					After:   models.JSONMap{"assigned_to": to},
					ActorID: actorID,
				})
			}
		}
		res := q.Updates(set)
		if res.Error != nil {
			return out, res.Error
		}
		*r.count(&out) = res.RowsAffected
		out.Total += res.RowsAffected
	}
	if out.Total == 0 {
		return out, nil
	}
	if len(perRecord) > 0 {
		if err := tx.CreateInBatches(&perRecord, reassignAuditBatchSize).Error; err != nil {
			return out, err
		}
	}
	after := models.JSONMap{
		"assigned_to": to, "deals": out.Deals, "leads": out.Leads,
		"prospects": out.Prospects, "tasks": out.Tasks,
	}
	return out, utils.WriteAuditLog(tx, "user", from, "records_reassigned",
		models.JSONMap{"assigned_to": from}, after, actorID)
}

// reassignResult reports a reassign_to hand-off in a user response.
type reassignResult struct {
	UserID     uint `json:"user_id"`
	ReassignTo uint `json:"reassign_to"`
	openRecordCounts
}

// userOpenRecords is one user's remaining open records in a bulk response.
type userOpenRecords struct {
	UserID uint `json:"user_id"`
	openRecordCounts
}

// errLastAdmin is returned (inside the write transaction) when a change
// would leave no active Admin; callers answer 409.
var errLastAdmin = errors.New("cannot demote, deactivate or delete the last active Admin")

// guardLastAdmin errors with errLastAdmin if, once every id in losing stops
// being an active Admin, none would be left. It locks the active Admin rows
// (FOR UPDATE) so two Admins demoting each other at once can't both pass.
// Run it inside the transaction that makes the change.
func guardLastAdmin(tx *gorm.DB, losing []uint) error {
	var admins []uint
	if err := tx.Model(&models.User{}).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("role = ? AND is_active = ?", models.RoleAdmin, true).
		Pluck("id", &admins).Error; err != nil {
		return err
	}
	affected, remaining := false, 0
	for _, id := range admins {
		if slices.Contains(losing, id) {
			affected = true
		} else {
			remaining++
		}
	}
	if affected && remaining == 0 {
		return errLastAdmin
	}
	return nil
}

// selfAccountError writes the 422 for an Admin changing their own role or
// deactivating/deleting themselves (another Admin must do it), naming field.
func selfAccountError(c *fiber.Ctx, field string) error {
	msg := "You cannot change your own role, deactivate or delete your own account"
	return utils.ValidationError(c, msg, map[string][]string{field: {msg}})
}

// validateReassignTo checks a deactivate/delete's reassign_to: an active
// sales-role user (validateAssignee) that isn't one of the users losing
// their records. Writes the 422 on field "reassign_to" and returns
// utils.ErrHandled, or nil if valid.
func validateReassignTo(c *fiber.Ctx, db *gorm.DB, to uint, losing []uint) error {
	msg := "reassign_to must be an active user in a sales role, other than the user(s) being deactivated"
	if slices.Contains(losing, to) {
		_ = utils.ValidationError(c, msg, map[string][]string{"reassign_to": {"invalid"}})
		return utils.ErrHandled
	}
	if err := validateAssignee(db, &to); err != nil {
		if errors.Is(err, errInvalidAssignee) {
			_ = utils.ValidationError(c, msg, map[string][]string{"reassign_to": {"invalid"}})
		} else {
			_ = utils.Internal(c, "Failed to validate reassign_to")
		}
		return utils.ErrHandled
	}
	return nil
}

// ownsPipelineRecords reports whether role may own Deals/Leads/Prospects.
func ownsPipelineRecords(role models.Role) bool {
	return slices.Contains(models.SalesPipelineRoles, role)
}

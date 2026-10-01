package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// merge.go — POST /companies/:id/merge and POST /contacts/:id/merge
// (api-system-spec.md §4/§5). :id is the record that survives; every
// source_ids record has its references re-pointed at it (merge_refs.go), its
// fields folded into the target's empty ones (merge_fill.go), and is then
// soft-deleted, all in one transaction.
//
// Lock order, to keep concurrent merges from deadlocking: the target and
// sources are locked FOR UPDATE in one statement ordered by id, then (Company
// merge only) the sources' Contacts are re-pointed before any other table,
// so a Company merge and a Contact merge running at once both take contacts
// rows before deals/tasks/activities rows.

// maxMergeSources caps source_ids: a merge is a manual clean-up of a handful
// of duplicates, and every source adds a round of re-pointing updates.
const maxMergeSources = 20

type mergeForm struct {
	SourceIDs []uint `json:"source_ids"`
}

// mergeConflict is a source value that was dropped because the target already
// has a different one for a field that identifies the record (Company
// website/domain, tax_id, branch_code; Contact email, phone).
type mergeConflict struct {
	Field    string `json:"field"`
	SourceID uint   `json:"source_id"`
	Value    string `json:"value"`
}

// mergeResponse is the 200 body's data for both merge endpoints. Target is a
// Company (with last_activity_at, as on GET /companies/:id) or a Contact.
type mergeResponse struct {
	Target    interface{}      `json:"target"`
	Moved     map[string]int64 `json:"moved"`
	Filled    []string         `json:"filled"`
	Conflicts []mergeConflict  `json:"conflicts"`
}

// errMergeNotFound carries the requested ids that don't exist or are
// soft-deleted, for the 404 message.
type errMergeNotFound struct{ ids []uint }

func (e *errMergeNotFound) Error() string { return "merge records not found: " + joinIDs(e.ids) }

func joinIDs(ids []uint) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(parts, ", ")
}

// parseMergeRequest reads :id and the body and runs the 422 checks. ok is
// false when a response has already been written.
func parseMergeRequest(c *fiber.Ctx, notFoundMsg string) (targetID uint, sourceIDs []uint, ok bool) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		_ = utils.NotFound(c, notFoundMsg)
		return 0, nil, false
	}
	var form mergeForm
	if err := c.BodyParser(&form); err != nil {
		_ = utils.BadRequest(c, "Invalid request body")
		return 0, nil, false
	}
	fail := func(msg string) (uint, []uint, bool) {
		_ = utils.ValidationError(c, msg, map[string][]string{"source_ids": {msg}})
		return 0, nil, false
	}
	switch {
	case len(form.SourceIDs) == 0:
		return fail("source_ids is required")
	case len(form.SourceIDs) > maxMergeSources:
		return fail(fmt.Sprintf("must not exceed %d ids", maxMergeSources))
	}
	seen := make(map[uint]bool, len(form.SourceIDs))
	for _, sid := range form.SourceIDs {
		if sid == uint(id) {
			return fail("must not contain the merge target")
		}
		if seen[sid] {
			return fail("must not contain duplicate ids")
		}
		seen[sid] = true
	}
	return uint(id), form.SourceIDs, true
}

// lockMergeRows loads target + sources FOR UPDATE, ordered by id (Postgres
// takes the row locks in that order, so two merges over overlapping ids
// queue instead of deadlocking). Soft-deleted rows are excluded by GORM's
// default scope, so a source a concurrent merge has just deleted shows up as
// missing. Returns the target and the sources in source_ids order.
func lockMergeRows[T any](tx *gorm.DB, targetID uint, sourceIDs []uint, idOf func(*T) uint) (*T, []*T, error) {
	ids := append([]uint{targetID}, sourceIDs...)
	var rows []T
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ?", ids).Order("id").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[uint]*T, len(rows))
	for i := range rows {
		byID[idOf(&rows[i])] = &rows[i]
	}
	var missing []uint
	for _, id := range ids {
		if byID[id] == nil {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		return nil, nil, &errMergeNotFound{ids: missing}
	}
	sources := make([]*T, len(sourceIDs))
	for i, sid := range sourceIDs {
		sources[i] = byID[sid]
	}
	return byID[targetID], sources, nil
}

// snapshotOf is v's JSON form as an audit-log map.
func snapshotOf(v interface{}) models.JSONMap {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out models.JSONMap
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// snapshotsOf is snapshotOf for each source, in order.
func snapshotsOf[T any](sources []*T) []models.JSONMap {
	out := make([]models.JSONMap, len(sources))
	for i, s := range sources {
		out[i] = snapshotOf(s)
	}
	return out
}

// writeMergeAudit writes the target's `merged` row and one `merged_into` row
// per source.
func writeMergeAudit(tx *gorm.DB, entityType string, targetID uint, before models.JSONMap,
	sourceIDs []uint, sourceSnapshots []models.JSONMap, res *mergeResponse, actorID uint) error {
	after := models.JSONMap{
		"source_ids": sourceIDs,
		"moved":      res.Moved,
		"filled":     res.Filled,
		"conflicts":  res.Conflicts,
	}
	if err := utils.WriteAuditLog(tx, entityType, targetID, "merged", before, after, actorID); err != nil {
		return err
	}
	for i, sid := range sourceIDs {
		if err := utils.WriteAuditLog(tx, entityType, sid, "merged_into", sourceSnapshots[i],
			models.JSONMap{"target_id": targetID}, actorID); err != nil {
			return err
		}
	}
	return nil
}

// softDeleteMergeSources stamps deleted_by and soft-deletes the sources in
// one statement. extra lets the Company merge clear the derived domain too.
func softDeleteMergeSources(tx *gorm.DB, model interface{}, sourceIDs []uint, actorID uint, extra map[string]interface{}) error {
	updates := map[string]interface{}{"deleted_by": actorID, "deleted_at": gorm.Expr("NOW()")}
	for k, v := range extra {
		updates[k] = v
	}
	return tx.Model(model).Where("id IN ?", sourceIDs).Updates(updates).Error
}

// writeMergeError maps a transaction error to the response.
func writeMergeError(c *fiber.Ctx, err error, label, failMsg string) error {
	var nf *errMergeNotFound
	if errors.As(err, &nf) {
		return utils.NotFound(c, fmt.Sprintf("%s not found: %s", label, joinIDs(nf.ids)))
	}
	return utils.Internal(c, failMsg)
}

// Merge godoc
// @Summary Merge duplicate companies into this one (Admin/Sales Manager only)
// @Description Moves everything that references each source Company (contacts, deals, leads, prospects, projects, customer products, company activities/attachments/tasks, lead referrals, dormant-company notification logs) to the target :id, fills the target's empty fields from the sources (in source_ids order), unions tags, then soft-deletes the sources. A source website/tax_id/branch_code that differs from the target's is not copied and is listed in conflicts. Writes a company/merged audit row on the target and company/merged_into on each source. One transaction.
// @Tags companies
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Target Company ID (survives)"
// @Param body body mergeForm true "1-20 source Company ids"
// @Success 200 {object} mergeResponse
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Failure 404 {object} map[string]interface{} "Target or a source doesn't exist or is deleted (message lists the ids)"
// @Failure 422 {object} map[string]interface{} "source_ids empty, over 20, contains the target, or has duplicates"
// @Router /companies/{id}/merge [post]
func (h *CompanyHandler) Merge(c *fiber.Ctx) error {
	targetID, sourceIDs, ok := parseMergeRequest(c, "Company not found")
	if !ok {
		return nil
	}
	actorID := middleware.CurrentUserID(c)
	var res mergeResponse
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		target, sources, err := lockMergeRows(tx, targetID, sourceIDs, func(m *models.Company) uint { return m.ID })
		if err != nil {
			return err
		}
		before := snapshotOf(target)
		sourceSnaps := snapshotsOf(sources)

		moved, err := repointCompanyRefs(tx, targetID, sourceIDs)
		if err != nil {
			return err
		}
		filled, conflicts := fillCompanyFields(target, sources)

		// Sources go first so a website/domain taken from one doesn't trip
		// the partial unique domain index (deleted rows are outside it). The
		// derived domain is cleared on them so restoring one from Trash can't
		// collide with the target either.
		if err := softDeleteMergeSources(tx, &models.Company{}, sourceIDs, actorID, map[string]interface{}{"domain": ""}); err != nil {
			return err
		}
		target.UpdatedBy = &actorID
		if err := tx.Save(target).Error; err != nil {
			return err
		}
		res = mergeResponse{Moved: moved, Filled: filled, Conflicts: conflicts}
		return writeMergeAudit(tx, "company", targetID, before, sourceIDs, sourceSnaps, &res, actorID)
	})
	if err != nil {
		return writeMergeError(c, err, "Company", "Failed to merge companies")
	}

	var company companyWithActivity
	if err := withLastActivityAt(h.DB.Model(&models.Company{})).
		Select("companies.*, last_company_activity.last_activity_at as last_activity_at").
		First(&company, targetID).Error; err != nil {
		return utils.Internal(c, "Failed to load merged company")
	}
	res.Target = company
	return utils.OK(c, res)
}

// Merge godoc
// @Summary Merge duplicate contacts into this one (Admin/Sales Manager only)
// @Description Moves everything that references each source Contact (deals, contact activities/tasks, lead referrals) to the target :id, fills the target's empty fields from the sources (in source_ids order), unions tags, then soft-deletes the sources. Sources may belong to other Companies; the target keeps its company_id. A source email/phone that differs from the target's is not copied and is listed in conflicts. Writes a contact/merged audit row on the target and contact/merged_into on each source. One transaction.
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Target Contact ID (survives)"
// @Param body body mergeForm true "1-20 source Contact ids"
// @Success 200 {object} mergeResponse
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Failure 404 {object} map[string]interface{} "Target or a source doesn't exist or is deleted (message lists the ids)"
// @Failure 422 {object} map[string]interface{} "source_ids empty, over 20, contains the target, or has duplicates"
// @Router /contacts/{id}/merge [post]
func (h *ContactHandler) Merge(c *fiber.Ctx) error {
	targetID, sourceIDs, ok := parseMergeRequest(c, "Contact not found")
	if !ok {
		return nil
	}
	actorID := middleware.CurrentUserID(c)
	var res mergeResponse
	var target *models.Contact
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		var sources []*models.Contact
		var err error
		target, sources, err = lockMergeRows(tx, targetID, sourceIDs, func(m *models.Contact) uint { return m.ID })
		if err != nil {
			return err
		}
		before := snapshotOf(target)
		sourceSnaps := snapshotsOf(sources)

		moved, err := repointContactRefs(tx, targetID, sourceIDs)
		if err != nil {
			return err
		}
		filled, conflicts := fillContactFields(target, sources)

		if err := softDeleteMergeSources(tx, &models.Contact{}, sourceIDs, actorID, nil); err != nil {
			return err
		}
		if target.IsPrimary {
			if err := clearOtherPrimaryContacts(tx, target.CompanyID, target.ID); err != nil {
				return err
			}
		}
		target.UpdatedBy = &actorID
		if err := tx.Save(target).Error; err != nil {
			return err
		}
		res = mergeResponse{Moved: moved, Filled: filled, Conflicts: conflicts}
		return writeMergeAudit(tx, "contact", targetID, before, sourceIDs, sourceSnaps, &res, actorID)
	})
	if err != nil {
		return writeMergeError(c, err, "Contact", "Failed to merge contacts")
	}
	res.Target = target
	return utils.OK(c, res)
}

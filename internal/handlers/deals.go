package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type DealHandler struct {
	DB *gorm.DB
}

func NewDealHandler(db *gorm.DB) *DealHandler {
	return &DealHandler{DB: db}
}

// List — GET /deals. Filters: stage, status, company_id, assigned_to,
// business_unit, channel, search (title).
func (h *DealHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := applyDealFilters(h.DB.Model(&models.Deal{}), c)

	var total int64
	query.Count(&total)

	var deals []models.Deal
	if joined, ok := utils.ApplyCompanyNameSort(query, "deals", c.Query("sort")); ok {
		query = joined
	} else {
		query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "title": true, "value": true}, "-created_at")
	}
	if err := query.Limit(perPage).Offset(offset).Find(&deals).Error; err != nil {
		return utils.Internal(c, "Failed to list deals")
	}
	return utils.List(c, deals, page, perPage, total)
}

type dealForm struct {
	CompanyID         uint                 `json:"company_id"`
	ContactID         uint                 `json:"contact_id"`
	Title             string               `json:"title"`
	Value             float64              `json:"value"`
	Stage             models.DealStage     `json:"stage"`
	Status            models.DealStatus    `json:"status"`
	ExpectedCloseDate *string              `json:"expected_close_date"`
	AssignedTo        *uint                `json:"assigned_to"`
	Channel           models.LeadSource    `json:"channel"`
	BusinessUnit      *models.BusinessUnit `json:"business_unit"`
	BusinessUnitItem  *string              `json:"business_unit_item"`
	Probability       *int                 `json:"probability"`
	LostReason        *models.LostReason   `json:"lost_reason"`
}

// validateStageAndChannel checks Stage/Channel against the active
// PipelineStage/LeadSourceOption rows — the DB-backed replacement for the
// previously hardcoded DealStage/LeadSource enum whitelist. An empty value is
// allowed through (defaulted downstream), and a value already stored on an
// existing row (e.g. the seeded hardcoded stage names) always validates fine.
//
// Returns utils.ErrHandled (never the nil c.JSON(...) forwards on its own) so
// callers' `if err != nil { return nil }` guard actually fires — see
// ErrHandled's doc for why forwarding ValidationError's own return value here
// would silently let every invalid stage/channel through.
func (h *DealHandler) validateStageAndChannel(c *fiber.Ctx, form dealForm) error {
	if !utils.IsActivePipelineStage(h.DB, string(form.Stage)) {
		_ = utils.ValidationError(c, "stage is not a valid active pipeline stage", map[string][]string{
			"stage": {"invalid"},
		})
		return utils.ErrHandled
	}
	if !utils.IsActiveLeadSource(h.DB, string(form.Channel)) {
		_ = utils.ValidationError(c, "channel is not a valid active lead source", map[string][]string{
			"channel": {"invalid"},
		})
		return utils.ErrHandled
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		_ = utils.ValidationError(c, "business_unit is invalid", map[string][]string{
			"business_unit": {"invalid"},
		})
		return utils.ErrHandled
	}
	return nil
}

// isLosingForm reports whether the submitted form is setting this Deal to
// Lost (by stage or by status directly) — the trigger for requiring
// lost_reason. Prefers the configured PipelineStage row's IsLostStage flag
// (via utils.IsLostStage) so an admin-renamed/custom Lost stage is still
// recognized, the same resolution DealHandler.UpdateStage already uses.
func isLosingForm(db *gorm.DB, form dealForm) bool {
	return utils.IsLostStage(db, form.Stage) || form.Status == models.DealStatusLost
}

// validateProbabilityAndLostReason mirrors the conditional-required pattern
// already used elsewhere in this codebase (e.g. lost_reason is only required
// once Stage/Status moves to Lost, the same way Contract-signed-before-Won-style
// gates only fire once their triggering condition is met). Returns
// utils.ErrHandled (see its doc) if invalid, nil if valid.
func validateProbabilityAndLostReason(c *fiber.Ctx, db *gorm.DB, form dealForm) error {
	if form.Probability != nil && (*form.Probability < 0 || *form.Probability > 100) {
		_ = utils.ValidationError(c, "probability must be between 0 and 100", map[string][]string{
			"probability": {"must be between 0 and 100"},
		})
		return utils.ErrHandled
	}
	if isLosingForm(db, form) {
		if form.LostReason == nil || *form.LostReason == "" {
			_ = utils.ValidationError(c, "lost_reason is required when marking a deal Lost", map[string][]string{
				"lost_reason": {"required"},
			})
			return utils.ErrHandled
		}
		if !models.IsValidLostReason(*form.LostReason) {
			_ = utils.ValidationError(c, "lost_reason is invalid", map[string][]string{
				"lost_reason": {"invalid"},
			})
			return utils.ErrHandled
		}
	}
	return nil
}

// isWinningForm reports whether the submitted form is setting this Deal to
// Won (by stage or by status directly) — the trigger for FR-CRM-045's
// signed-contract precondition. Mirrors isLosingForm's resolution.
func isWinningForm(db *gorm.DB, form dealForm) bool {
	return utils.IsWonStage(db, form.Stage) || form.Status == models.DealStatusWon
}

// validateContractSignedBeforeWon enforces FR-CRM-045 ("configurable, not
// hard-enforced by default" — so it's a no-op unless an Admin has turned it
// on via AppSettings). Once enabled, a Deal can only move into Won if it
// already has at least one Contract with status Signed. dealID is 0 for a
// brand-new Deal being created directly in a Won stage/status — which can
// never have an existing Contract yet, so the same Count query correctly
// blocks that case too, with no special-casing needed.
func validateContractSignedBeforeWon(c *fiber.Ctx, db *gorm.DB, dealID uint) error {
	settings := utils.GetAppSettings(db)
	if !settings.RequireSignedContractBeforeWon {
		return nil
	}
	var count int64
	db.Model(&models.Contract{}).Where("deal_id = ? AND status = ?", dealID, models.ContractStatusSigned).Count(&count)
	if count == 0 {
		_ = utils.ValidationError(c, "a signed contract is required before marking this deal Won", map[string][]string{
			"stage": {"requires_signed_contract"},
		})
		return utils.ErrHandled
	}
	return nil
}

// validateDealRequiredFields checks the three dealForm fields Create and
// Update both insist on (company_id, contact_id, title) — extracted since the
// two handlers previously duplicated this exact check verbatim. Returns
// utils.ErrHandled (see its doc) if invalid, nil if valid.
func validateDealRequiredFields(c *fiber.Ctx, form dealForm) error {
	if form.Title == "" || form.CompanyID == 0 || form.ContactID == 0 {
		_ = utils.ValidationError(c, "company_id, contact_id and title are required", map[string][]string{
			"company_id": {"required"},
			"contact_id": {"required"},
			"title":      {"required"},
		})
		return utils.ErrHandled
	}
	return nil
}

// validateDealValueAndDate checks the two dealForm fields that previously had
// no format/range validation at all: Value (must be non-negative — a client
// bug or bad import row supplying a negative number would silently corrupt
// every value-sum aggregate on the dashboard) and ExpectedCloseDate (must
// parse as either a plain "YYYY-MM-DD" date or a full RFC3339 timestamp — the
// two shapes the frontend actually sends, see forecastTrend's comment). A
// malformed date string would otherwise persist untouched and silently fail
// to land in any forecastTrend month bucket instead of erroring at write time.
// Returns utils.ErrHandled (see its doc) if invalid, nil if valid.
func validateDealValueAndDate(c *fiber.Ctx, form dealForm) error {
	if form.Value < 0 {
		_ = utils.ValidationError(c, "value must not be negative", map[string][]string{
			"value": {"must not be negative"},
		})
		return utils.ErrHandled
	}
	if form.ExpectedCloseDate != nil && *form.ExpectedCloseDate != "" {
		date := *form.ExpectedCloseDate
		if _, err := time.Parse("2006-01-02", date); err != nil {
			if _, err := time.Parse(time.RFC3339, date); err != nil {
				_ = utils.ValidationError(c, "expected_close_date must be a valid date", map[string][]string{
					"expected_close_date": {"invalid"},
				})
				return utils.ErrHandled
			}
		}
	}
	return nil
}

// defaultProbabilityFor resolves the win-probability default for a stage via
// utils.StageDefaultProbability, which prefers the configured PipelineStage
// row (Won/Lost flags -> 100/0, in-between stages -> interpolated by
// sort_order) over the hardcoded models.StageDefaultProbability switch, so a
// custom Admin-added stage — or a renamed Won/Lost stage — still gets a
// sensible default instead of a flat 10.
func (h *DealHandler) defaultProbabilityFor(stage models.DealStage) int {
	return utils.StageDefaultProbability(h.DB, stage)
}

// syncStatusWithStageFlags forces deal.Status to won/lost whenever the
// deal's current Stage resolves (via utils.IsWonStage/IsLostStage) to a
// won/lost stage, regardless of what Status the request body supplied.
// Mirrors UpdateStage's isWon/isLost handling so Create/Update (the full
// form) can't drift out of sync with the Kanban quick-move endpoint — e.g. a
// client submitting stage=<custom Lost stage> with status="open" would
// otherwise be miscounted as open in forecast/report aggregates.
func (h *DealHandler) syncStatusWithStageFlags(deal *models.Deal) {
	switch {
	case utils.IsWonStage(h.DB, deal.Stage):
		deal.Status = models.DealStatusWon
	case utils.IsLostStage(h.DB, deal.Stage):
		deal.Status = models.DealStatusLost
	}
}

// Create — POST /deals.
func (h *DealHandler) Create(c *fiber.Ctx) error {
	var form dealForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := validateDealRequiredFields(c, form); err != nil {
		return nil
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a deal to another sales rep")
	}
	if err := validateDealValueAndDate(c, form); err != nil {
		return nil
	}
	if err := validateProbabilityAndLostReason(c, h.DB, form); err != nil {
		return nil
	}
	if isWinningForm(h.DB, form) {
		// dealID 0 — a brand-new Deal can't have a Contract yet, so this only
		// ever matters (and always blocks) when the toggle is enabled.
		if err := validateContractSignedBeforeWon(c, h.DB, 0); err != nil {
			return nil
		}
	}
	if err := h.validateStageAndChannel(c, form); err != nil {
		return nil
	}

	deal := models.Deal{
		CompanyID: form.CompanyID, ContactID: form.ContactID, Title: form.Title, Value: form.Value,
		Stage: form.Stage, Status: form.Status, ExpectedCloseDate: form.ExpectedCloseDate,
		AssignedTo: form.AssignedTo, Channel: form.Channel,
		BusinessUnit: form.BusinessUnit, BusinessUnitItem: form.BusinessUnitItem,
		Probability: form.Probability, LostReason: form.LostReason,
	}
	if deal.Stage == "" {
		deal.Stage = models.DealStageLead
	}
	if deal.Status == "" {
		deal.Status = models.DealStatusOpen
	}
	// Keep Status in sync with the resolved stage flags — otherwise a client
	// could submit a custom Lost/Won stage alongside status "open" and the
	// deal would be excluded from won/lost dashboards (forecast revenue,
	// reports) despite sitting in a terminal stage.
	h.syncStatusWithStageFlags(&deal)
	if deal.Probability == nil {
		def := h.defaultProbabilityFor(deal.Stage)
		deal.Probability = &def
	}
	if err := h.DB.Create(&deal).Error; err != nil {
		return utils.Internal(c, "Failed to create deal")
	}
	return utils.Created(c, deal)
}

// Get — GET /deals/:id.
func (h *DealHandler) Get(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	return utils.OK(c, deal)
}

// Update — PUT /deals/:id.
func (h *DealHandler) Update(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	if !CanWrite(c, deal.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this deal")
	}

	var form dealForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a deal to another sales rep")
	}
	if err := validateDealRequiredFields(c, form); err != nil {
		return nil
	}
	if err := validateDealValueAndDate(c, form); err != nil {
		return nil
	}
	if err := validateProbabilityAndLostReason(c, h.DB, form); err != nil {
		return nil
	}
	// Only check on the actual transition into Won, not on every subsequent
	// save of a deal that's already Won — the frontend's Overview form
	// resubmits the deal's current stage/status on every save (even an
	// unrelated field edit), and deal.Status here is still the pre-mutation
	// value, so this only fires once per Won transition.
	if isWinningForm(h.DB, form) && deal.Status != models.DealStatusWon {
		if err := validateContractSignedBeforeWon(c, h.DB, deal.ID); err != nil {
			return nil
		}
	}
	if err := h.validateStageAndChannel(c, form); err != nil {
		return nil
	}

	deal.CompanyID, deal.ContactID, deal.Title, deal.Value = form.CompanyID, form.ContactID, form.Title, form.Value
	deal.Stage, deal.Status, deal.ExpectedCloseDate = form.Stage, form.Status, form.ExpectedCloseDate
	deal.AssignedTo, deal.Channel = form.AssignedTo, form.Channel
	deal.BusinessUnit, deal.BusinessUnitItem = form.BusinessUnit, form.BusinessUnitItem
	deal.Probability, deal.LostReason = form.Probability, form.LostReason
	// Keep Status in sync with the resolved stage flags — see Create's comment.
	h.syncStatusWithStageFlags(&deal)
	if deal.Probability == nil {
		def := h.defaultProbabilityFor(deal.Stage)
		deal.Probability = &def
	}
	// LostReason only makes sense while the deal is actually Lost — clear a
	// stale reason left over from a previous Lost stint once it moves elsewhere.
	// Uses the same PipelineStage-flag-aware resolution as isLosingForm/UpdateStage
	// so a renamed/custom Lost stage doesn't silently lose its lost_reason.
	if !utils.IsLostStage(h.DB, deal.Stage) && deal.Status != models.DealStatusLost {
		deal.LostReason = nil
	}

	if err := h.DB.Save(&deal).Error; err != nil {
		return utils.Internal(c, "Failed to update deal")
	}
	return utils.OK(c, deal)
}

// Delete — DELETE /deals/:id. Soft-delete (AuditedModel) — recoverable via
// Restore/Trash below.
func (h *DealHandler) Delete(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	if !CanWrite(c, deal.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this deal")
	}
	actorID := middleware.CurrentUserID(c)
	if err := utils.GenericSoftDelete(h.DB, &deal, actorID); err != nil {
		return utils.Internal(c, "Failed to delete deal")
	}
	return utils.NoContent(c)
}

// Trash — GET /deals/trash. Sales-Manager/Admin only (route-gated).
func (h *DealHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Deal](c, h.DB, "Failed to list deleted deals")
}

// Restore — POST /deals/:id/restore. Sales-Manager/Admin only (route-gated).
func (h *DealHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Deal](c, h.DB, "Deleted deal not found", "Failed to restore deal")
}

type bulkIDsForm struct {
	IDs []uint `json:"ids"`
}

type bulkReassignForm struct {
	IDs        []uint `json:"ids"`
	AssignedTo *uint  `json:"assigned_to"`
}

type bulkTagForm struct {
	IDs  []uint   `json:"ids"`
	Tags []string `json:"tags"`
	Mode string   `json:"mode"` // "add" (default) or "set"
}

// BulkReassign — PATCH /deals/bulk-reassign. Sales-Manager/Admin only (route-gated).
func (h *DealHandler) BulkReassign(c *fiber.Ctx) error {
	var form bulkReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "deal", "bulk_reassigned", actorID,
		func(tx *gorm.DB, deal *models.Deal) (models.JSONMap, models.JSONMap, error) {
			before := models.JSONMap{"assigned_to": deal.AssignedTo}
			deal.AssignedTo = form.AssignedTo
			after := models.JSONMap{"assigned_to": deal.AssignedTo}
			return before, after, tx.Save(deal).Error
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk reassign deals")
	}
	return utils.NoContent(c)
}

// BulkTag — PATCH /deals/bulk-tag. Sales-Manager/Admin only (route-gated).
func (h *DealHandler) BulkTag(c *fiber.Ctx) error {
	var form bulkTagForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "deal", "bulk_tagged", actorID,
		func(tx *gorm.DB, deal *models.Deal) (models.JSONMap, models.JSONMap, error) {
			before := models.JSONMap{"tags": []string(deal.Tags)}
			if form.Mode == "set" {
				deal.Tags = form.Tags
			} else {
				deal.Tags = mergeTags(deal.Tags, form.Tags)
			}
			after := models.JSONMap{"tags": []string(deal.Tags)}
			return before, after, tx.Save(deal).Error
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk tag deals")
	}
	return utils.NoContent(c)
}

// BulkArchive — PATCH /deals/bulk-archive. Sales-Manager/Admin only (route-gated).
// Soft-deletes each deal (same as Delete), in one transaction.
func (h *DealHandler) BulkArchive(c *fiber.Ctx) error {
	var form bulkIDsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "deal", "bulk_archived", actorID,
		func(tx *gorm.DB, deal *models.Deal) (models.JSONMap, models.JSONMap, error) {
			if err := tx.Model(deal).Update("deleted_by", actorID).Error; err != nil {
				return nil, nil, err
			}
			err := tx.Delete(deal).Error
			return models.JSONMap{"deleted_at": nil}, models.JSONMap{"deleted_by": actorID}, err
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk archive deals")
	}
	return utils.NoContent(c)
}

// mergeTags appends tags not already present, case-sensitively, preserving order.
func mergeTags(existing []string, add []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, t := range existing {
		seen[t] = true
	}
	merged := append([]string{}, existing...)
	for _, t := range add {
		if !seen[t] {
			merged = append(merged, t)
			seen[t] = true
		}
	}
	return merged
}

type dealStageForm struct {
	Stage models.DealStage `json:"stage"`
}

// UpdateStage — PATCH /deals/:id/stage. Body: {stage}. Sets status to
// won/lost alongside stage in the same transaction; writes an audit log entry
// per §8.5's explicit minimum scope (stage changes).
func (h *DealHandler) UpdateStage(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	if !CanWrite(c, deal.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this deal")
	}

	var form dealStageForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Stage == "" {
		return utils.ValidationError(c, "stage is required", map[string][]string{"stage": {"required"}})
	}
	if !utils.IsActivePipelineStage(h.DB, string(form.Stage)) {
		return utils.ValidationError(c, "stage is not a valid active pipeline stage", map[string][]string{"stage": {"invalid"}})
	}

	// isWon/isLost prefer the configured PipelineStage row's flags (so a custom,
	// admin-added stage can behave like Won/Lost without being named exactly
	// that) — falling back to the hardcoded name match if no row exists yet,
	// e.g. right after a migration and before the seed runs. Shared with
	// Create/Update's syncStatusWithStageFlags/defaultProbabilityFor via
	// utils.IsWonStage/IsLostStage so both endpoints resolve stages the same way.
	isWon := utils.IsWonStage(h.DB, form.Stage)
	isLost := utils.IsLostStage(h.DB, form.Stage)

	before := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	oldStage := deal.Stage
	deal.Stage = form.Stage
	switch {
	case isWon:
		// Only check on the actual transition into Won, not on every
		// re-affirming move within the Won stage (e.g. dragging the card to a
		// different position within the same Won column) — deal.Status here
		// is still the pre-mutation value.
		if deal.Status != models.DealStatusWon {
			if err := validateContractSignedBeforeWon(c, h.DB, deal.ID); err != nil {
				return nil
			}
		}
		deal.Status = models.DealStatusWon
		// Hook point: FR-CRM-064 auto-creates/updates a CustomerProduct(status: Active)
		// per Product on this Deal's accepted Quote — deferred until Quotes have a
		// real "accepted" flow to hang the side effect off.
	case isLost:
		deal.Status = models.DealStatusLost
	default:
		if deal.Status != models.DealStatusWon && deal.Status != models.DealStatusLost {
			deal.Status = models.DealStatusOpen
		}
	}
	// Re-derive probability for the new stage on every drag/quick-move (Kanban
	// has no probability input of its own) — the Deal's Overview tab can still
	// override it manually afterwards. lost_reason isn't collected by this
	// quick-move endpoint (only the full Update form validates it as
	// required-when-Lost) so it's deliberately left untouched here.
	if oldStage != deal.Stage {
		def := h.defaultProbabilityFor(deal.Stage)
		deal.Probability = &def
	}

	after := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Save(&deal).Error },
		oldStage != deal.Stage, "deal", deal.ID, "stage_changed", before, after, middleware.CurrentUserID(c))
	if err != nil {
		return utils.Internal(c, "Failed to update deal stage")
	}
	return utils.OK(c, deal)
}

type dealReassignForm struct {
	AssignedTo *uint `json:"assigned_to"`
}

// Reassign — PATCH /deals/:id/reassign. Sales-Manager/Admin only (route-gated).
func (h *DealHandler) Reassign(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}

	var form dealReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	before := models.JSONMap{"assigned_to": deal.AssignedTo}
	deal.AssignedTo = form.AssignedTo
	after := models.JSONMap{"assigned_to": deal.AssignedTo}

	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Save(&deal).Error },
		true, "deal", deal.ID, "reassigned", before, after, middleware.CurrentUserID(c))
	if err != nil {
		return utils.Internal(c, "Failed to reassign deal")
	}
	return utils.OK(c, deal)
}

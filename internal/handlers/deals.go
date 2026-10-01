package handlers

import (
	"fmt"
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

// List godoc
// @Summary List deals (Admin/Sales Rep/Sales Manager)
// @Description Returns deals, filterable by stage, status, company_id, assigned_to, business_unit, channel, and search (title). Backs the Kanban board and dashboard. api-system-spec.md §7.1.
// @Tags deals
// @Security BearerAuth
// @Produce json
// @Param stage query string false "Filter by DealStage"
// @Param status query string false "Filter by DealStatus (open/won/lost)"
// @Param company_id query int false "Filter by Company ID"
// @Param assigned_to query int false "Filter by assigned Sales Rep user ID"
// @Param business_unit query string false "Filter by BusinessUnit"
// @Param channel query string false "Filter by lead source channel"
// @Param search query string false "Search by title"
// @Param sort query string false "Sort field (created_at, title, value, company_name), prefix - for descending"
// @Success 200 {object} map[string]interface{} "Paginated deal list (data, page, per_page, total)"
// @Router /deals [get]
func (h *DealHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := applyDealFilters(h.DB.Model(&models.Deal{}), c)

	var total int64
	query.Count(&total)

	var deals []models.Deal
	if joined, ok := utils.ApplyCompanyNameSort(query, "deals", c.Query("sort")); ok {
		query = joined
	} else {
		query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "title": true, "value": true, "position": true}, "-created_at")
	}
	if err := query.Limit(perPage).Offset(offset).Find(&deals).Error; err != nil {
		return utils.Internal(c, "Failed to list deals")
	}
	return utils.List(c, deals, page, perPage, total)
}

// dealFields are the Deal fields Lead Convert's `deal` object accepts too;
// dealForm embeds them, so both bodies share one JSON shape and one
// validation chain.
type dealFields struct {
	Title             string               `json:"title"`
	Value             float64              `json:"value"`
	Stage             models.DealStage     `json:"stage"`
	ExpectedCloseDate *string              `json:"expected_close_date"`
	AssignedTo        *uint                `json:"assigned_to"`
	Channel           models.LeadSource    `json:"channel"`
	BusinessUnit      *models.BusinessUnit `json:"business_unit"`
	BusinessUnitItem  *string              `json:"business_unit_item"`
	// LostReason is required once stage or status is Lost.
	LostReason *models.LostReason `json:"lost_reason"`
}

type dealForm struct {
	dealFields
	CompanyID        uint                     `json:"company_id"`
	ContactID        uint                     `json:"contact_id"`
	Status           models.DealStatus        `json:"status"`
	Probability      *int                     `json:"probability"`
	ForecastCategory *models.ForecastCategory `json:"forecast_category"`
}

// validateStageAndChannel checks Stage/Channel against the active
// PipelineStage/LeadSourceOption rows (empty is allowed, defaulted
// downstream) and BusinessUnit against its enum. to is form.Stage's flags.
//
// Returns utils.ErrHandled rather than ValidationError's own (nil) result so
// callers' `if err != nil` guard fires — see ErrHandled's doc.
func validateStageAndChannel(c *fiber.Ctx, db *gorm.DB, form dealForm, to utils.StageFlags) error {
	if form.Stage != "" && !to.Active {
		_ = utils.ValidationError(c, "stage is not a valid active pipeline stage", map[string][]string{
			"stage": {"invalid"},
		})
		return utils.ErrHandled
	}
	if !utils.IsActiveLeadSource(db, string(form.Channel)) {
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

// isLosingForm reports whether the form sets the Deal to Lost, by stage
// (to is form.Stage's flags) or by status — the trigger for requiring
// lost_reason.
func isLosingForm(form dealForm, to utils.StageFlags) bool {
	return to.Lost || form.Status == models.DealStatusLost
}

// validateProbabilityAndLostReason range-checks probability, checks
// forecast_category, and requires a valid lost_reason once the form is
// losing. Returns utils.ErrHandled (see its doc) if invalid, nil if valid.
func validateProbabilityAndLostReason(c *fiber.Ctx, form dealForm, to utils.StageFlags) error {
	if form.Probability != nil && (*form.Probability < 0 || *form.Probability > 100) {
		_ = utils.ValidationError(c, "probability must be between 0 and 100", map[string][]string{
			"probability": {"must be between 0 and 100"},
		})
		return utils.ErrHandled
	}
	if form.ForecastCategory != nil && *form.ForecastCategory != "" && !models.IsValidForecastCategory(*form.ForecastCategory) {
		_ = utils.ValidationError(c, "forecast_category is invalid", map[string][]string{
			"forecast_category": {"invalid"},
		})
		return utils.ErrHandled
	}
	if isLosingForm(form, to) {
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

// isWinningForm reports whether the form sets the Deal to Won, by stage or
// by status — the trigger for FR-CRM-045's signed-contract precondition.
func isWinningForm(form dealForm, to utils.StageFlags) bool {
	return to.Won || form.Status == models.DealStatusWon
}

// validateContractSignedBeforeWon enforces FR-CRM-045 when an Admin has
// enabled it in AppSettings (off by default): a Deal can only move into Won
// with at least one Signed Contract. dealID 0 (a Deal not created yet) has
// none, so the gate always blocks it.
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

// validateDealRequiredFields checks company_id, contact_id and title, which
// Create and Update both require. Returns utils.ErrHandled (see its doc) if
// invalid, nil if valid.
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

// validateDealValueAndDate rejects a negative Value (it would skew every
// value-sum aggregate) and an ExpectedCloseDate that isn't YYYY-MM-DD or
// RFC3339 (it would never land in a forecastTrend month bucket). Returns
// utils.ErrHandled (see its doc) if invalid, nil if valid.
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

// validateNewDealForm is the check chain for a Deal that doesn't exist yet
// (Deal Create, Lead Convert), after any required-field check: assignee,
// value/date, probability/lost_reason, the signed-contract gate on a Won
// start, and stage/channel/business_unit. Returns form.Stage's flags for
// the caller to resolve status with, and utils.ErrHandled once a response
// has been written.
func validateNewDealForm(c *fiber.Ctx, db *gorm.DB, form dealForm) (utils.StageFlags, error) {
	to := utils.LookupStageFlags(db, form.Stage)
	if !CanWrite(c, form.AssignedTo) {
		_ = utils.Forbidden(c, "Cannot assign a deal to another sales rep")
		return to, utils.ErrHandled
	}
	if err := validateAssignee(db, form.AssignedTo); err != nil {
		_ = respondAssigneeErr(c, err)
		return to, utils.ErrHandled
	}
	if err := validateDealValueAndDate(c, form); err != nil {
		return to, err
	}
	if err := validateProbabilityAndLostReason(c, form, to); err != nil {
		return to, err
	}
	if isWinningForm(form, to) {
		if err := validateContractSignedBeforeWon(c, db, 0); err != nil {
			return to, err
		}
	}
	return to, validateStageAndChannel(c, db, form, to)
}

// defaultProbabilityFor resolves the win-probability default for a stage
// (see utils.StageDefaultProbability).
func (h *DealHandler) defaultProbabilityFor(stage models.DealStage) int {
	return utils.StageDefaultProbability(h.DB, stage)
}

// defaultForecastCategoryFor resolves the Commit/Best Case/Pipeline default
// for a stage. Unlike probability it isn't configurable per PipelineStage —
// the fixed three-way grouping is enough.
func (h *DealHandler) defaultForecastCategoryFor(stage models.DealStage) models.ForecastCategory {
	return models.StageDefaultForecastCategory(stage)
}

// resolveDealStatus is the one rule for a Deal's status given the stage it
// is moving from and to: a Won/Lost stage forces won/lost (so a custom Lost
// stage with status "open" can't be miscounted in forecasts); leaving a
// Won/Lost stage for an open one reopens the deal; otherwise the requested
// status stands, so "lost at an open stage" is honored. clearLostReason is
// true whenever the result isn't lost — the reason belongs to a Lost stint.
func resolveDealStatus(from, to utils.StageFlags, stageChanged bool, requested models.DealStatus) (status models.DealStatus, clearLostReason bool) {
	switch {
	case to.Won:
		status = models.DealStatusWon
	case to.Lost:
		status = models.DealStatusLost
	case stageChanged && from.Terminal():
		status = models.DealStatusOpen
	default:
		status = requested
	}
	return status, status != models.DealStatusLost
}

// Create godoc
// @Summary Create a deal (Admin/Sales Rep/Sales Manager)
// @Description Creates a Deal. value must be >= 0; expected_close_date, if supplied, must parse as YYYY-MM-DD or RFC3339; stage/channel must be an active PipelineStage/LeadSourceOption; probability (if supplied) must be 0-100; lost_reason is required once stage/status moves to Lost; a Sales Rep cannot assign to another rep. api-system-spec.md §7.1.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body dealForm true "Deal fields"
// @Success 201 {object} models.Deal
// @Failure 400 {object} map[string]interface{} "Invalid request body, or validation error (required fields, value, expected_close_date, probability, lost_reason, stage/channel/business_unit)"
// @Failure 403 {object} map[string]interface{} "Cannot assign a deal to another sales rep"
// @Failure 422 {object} map[string]interface{} "assigned_to is not an active sales-role user"
// @Router /deals [post]
func (h *DealHandler) Create(c *fiber.Ctx) error {
	var form dealForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := validateDealRequiredFields(c, form); err != nil {
		return nil
	}
	to, err := validateNewDealForm(c, h.DB, form)
	if err != nil {
		return nil
	}

	deal := models.Deal{
		CompanyID: form.CompanyID, ContactID: form.ContactID, Title: form.Title, Value: form.Value,
		Stage: form.Stage, Status: form.Status, ExpectedCloseDate: form.ExpectedCloseDate,
		AssignedTo: form.AssignedTo, Channel: form.Channel,
		BusinessUnit: form.BusinessUnit, BusinessUnitItem: form.BusinessUnitItem,
		Probability: form.Probability, LostReason: form.LostReason,
		ForecastCategory: form.ForecastCategory,
	}
	// The default stage is never Won/Lost, matching an empty stage's flags.
	if deal.Stage == "" {
		deal.Stage = utils.DefaultPipelineStage(h.DB)
	}
	if deal.Status == "" {
		deal.Status = models.DealStatusOpen
	}
	deal.Status, _ = resolveDealStatus(to, to, false, deal.Status)
	if deal.Probability == nil {
		def := h.defaultProbabilityFor(deal.Stage)
		deal.Probability = &def
	}
	if deal.ForecastCategory == nil || *deal.ForecastCategory == "" {
		def := h.defaultForecastCategoryFor(deal.Stage)
		deal.ForecastCategory = &def
	}
	deal.Position = dealLanes.next(h.DB, deal.Stage)
	if err := h.DB.Create(&deal).Error; err != nil {
		return utils.Internal(c, "Failed to create deal")
	}
	return utils.Created(c, deal)
}

// Get godoc
// @Summary Get a deal (Admin/Sales Rep/Sales Manager)
// @Description Returns a single Deal by ID.
// @Tags deals
// @Security BearerAuth
// @Produce json
// @Param id path int true "Deal ID"
// @Success 200 {object} models.Deal
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{id} [get]
func (h *DealHandler) Get(c *fiber.Ctx) error {
	var deal models.Deal
	if err := utils.FindByID(c, h.DB, &deal, "Deal not found"); err != nil {
		return nil
	}
	return utils.OK(c, deal)
}

// Update godoc
// @Summary Update a deal (Admin/Sales Rep/Sales Manager)
// @Description Full update of a Deal — same validation as Create. An omitted stage/status keeps the stored value; moving from a Won/Lost stage to an open one sets status open. Writes a stage_changed audit log entry when the submitted stage differs from the deal's current one. Only the assigned Sales Rep (or Admin/Sales Manager) may update; a Sales Rep may keep or claim the deal but not reassign it to another rep or unassign it (403). A changed assigned_to must be an active sales-role user (422). value/company_id changes write an updated audit entry and an assigned_to change a reassigned one. api-system-spec.md §7.1.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Deal ID"
// @Param body body dealForm true "Deal fields"
// @Success 200 {object} models.Deal
// @Failure 400 {object} map[string]interface{} "Invalid request body, or validation error (required fields, value, expected_close_date, probability, lost_reason, stage/channel/business_unit)"
// @Failure 403 {object} map[string]interface{} "Not authorized to update this deal, or cannot assign a deal to another sales rep or unassign it"
// @Failure 422 {object} map[string]interface{} "assigned_to is not an active sales-role user"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{id} [put]
func (h *DealHandler) Update(c *fiber.Ctx) error {
	var deal models.Deal
	if err := utils.FindByID(c, h.DB, &deal, "Deal not found"); err != nil {
		return nil
	}
	if !CanWrite(c, deal.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this deal")
	}

	var form dealForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	// An omitted stage/status keeps the stored one. Filled in before
	// validation so the Won/Lost gates below see the deal's real state.
	if form.Stage == "" {
		form.Stage = deal.Stage
	}
	if form.Status == "" {
		form.Status = deal.Status
	}
	oldStage := deal.Stage
	stageChanged := form.Stage != oldStage
	to := utils.LookupStageFlags(h.DB, form.Stage)
	from := to
	if stageChanged {
		from = utils.LookupStageFlags(h.DB, oldStage)
	}
	status, clearLostReason := resolveDealStatus(from, to, stageChanged, form.Status)
	// The form resubmits the current status on every save, so a reopen
	// (leaving Won/Lost for an open stage) must be what validation sees —
	// otherwise a Lost deal moved back would still demand a lost_reason.
	// A Won/Lost destination is checked through its flags instead.
	if !to.Terminal() {
		form.Status = status
	}
	// A Sales Rep may keep or claim the deal, not unassign it or hand it on.
	// An unchanged assignee isn't re-validated, so a deal still owned by a
	// since-deactivated user stays editable.
	assigneeChanged := !sameAssignee(deal.AssignedTo, form.AssignedTo)
	if !CanSetAssignee(c, deal.AssignedTo, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a deal to another sales rep or unassign it")
	}
	if assigneeChanged {
		if err := validateAssignee(h.DB, form.AssignedTo); err != nil {
			return respondAssigneeErr(c, err)
		}
	}
	if err := validateDealRequiredFields(c, form); err != nil {
		return nil
	}
	if err := validateDealValueAndDate(c, form); err != nil {
		return nil
	}
	if err := validateProbabilityAndLostReason(c, form, to); err != nil {
		return nil
	}
	// Only on the transition into Won (deal.Status is still the stored
	// value), not on every resubmitting save of an already-Won deal.
	if isWinningForm(form, to) && deal.Status != models.DealStatusWon {
		if err := validateContractSignedBeforeWon(c, h.DB, deal.ID); err != nil {
			return nil
		}
	}
	if err := validateStageAndChannel(c, h.DB, form, to); err != nil {
		return nil
	}

	before := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	// value/company_id changes get an "updated" row, an owner change a
	// "reassigned" one (same shape as PATCH /deals/:id/reassign).
	fieldsBefore, fieldsAfter := models.JSONMap{}, models.JSONMap{}
	if deal.Value != form.Value {
		fieldsBefore["value"], fieldsAfter["value"] = deal.Value, form.Value
	}
	if deal.CompanyID != form.CompanyID {
		fieldsBefore["company_id"], fieldsAfter["company_id"] = deal.CompanyID, form.CompanyID
	}
	ownerBefore := models.JSONMap{"assigned_to": deal.AssignedTo}

	deal.CompanyID, deal.ContactID, deal.Title, deal.Value = form.CompanyID, form.ContactID, form.Title, form.Value
	deal.Stage, deal.Status, deal.ExpectedCloseDate = form.Stage, status, form.ExpectedCloseDate
	deal.AssignedTo, deal.Channel = form.AssignedTo, form.Channel
	deal.BusinessUnit, deal.BusinessUnitItem = form.BusinessUnit, form.BusinessUnitItem
	deal.Probability, deal.LostReason = form.Probability, form.LostReason
	deal.ForecastCategory = form.ForecastCategory
	if clearLostReason {
		deal.LostReason = nil
	}
	if deal.Probability == nil {
		def := h.defaultProbabilityFor(deal.Stage)
		deal.Probability = &def
	}
	if deal.ForecastCategory == nil || *deal.ForecastCategory == "" {
		def := h.defaultForecastCategoryFor(deal.Stage)
		deal.ForecastCategory = &def
	}
	if stageChanged {
		deal.MarkStageEntered(string(oldStage))
		// No drag geometry on the edit form — append to the new lane's end.
		deal.Position = dealLanes.next(h.DB, deal.Stage)
	}

	// A stage change here gets the same stage_changed audit row and
	// company Activity (a stage move counts as customer contact) as
	// UpdateStage, feeding the audit viewer and Pipeline History.
	after := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error {
		if err := tx.Save(&deal).Error; err != nil {
			return err
		}
		actorID := middleware.CurrentUserID(c)
		if len(fieldsBefore) > 0 {
			if err := utils.WriteAuditLog(tx, "deal", deal.ID, "updated", fieldsBefore, fieldsAfter, actorID); err != nil {
				return err
			}
		}
		if assigneeChanged {
			if err := utils.WriteAuditLog(tx, "deal", deal.ID, "reassigned", ownerBefore,
				models.JSONMap{"assigned_to": deal.AssignedTo}, actorID); err != nil {
				return err
			}
		}
		if stageChanged {
			subject := fmt.Sprintf("Deal stage changed: %s → %s", oldStage, deal.Stage)
			return utils.LogCompanyActivity(tx, deal.CompanyID, subject, middleware.CurrentUserID(c))
		}
		return nil
	}, stageChanged, "deal", deal.ID, "stage_changed", before, after, middleware.CurrentUserID(c))
	if err != nil {
		return utils.Internal(c, "Failed to update deal")
	}
	return utils.OK(c, deal)

}

// Delete godoc
// @Summary Delete a deal (Admin/Sales Rep/Sales Manager)
// @Description Soft-delete (AuditedModel) — recoverable via Restore/Trash below. Only the assigned Sales Rep (or Admin/Sales Manager) may delete.
// @Tags deals
// @Security BearerAuth
// @Param id path int true "Deal ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to delete this deal"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Router /deals/{id} [delete]
func (h *DealHandler) Delete(c *fiber.Ctx) error {
	var deal models.Deal
	if err := utils.FindByID(c, h.DB, &deal, "Deal not found"); err != nil {
		return nil
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

// Trash godoc
// @Summary List deleted deals (Admin/Sales Manager)
// @Description Returns soft-deleted Deals, paginated like GET /deals.
// @Tags deals
// @Security BearerAuth
// @Produce json
// @Param search query string false "Search by title"
// @Success 200 {object} map[string]interface{} "Paginated deal list (data, page, per_page, total)"
// @Router /deals/trash [get]
func (h *DealHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Deal](c, h.DB, "Failed to list deleted deals", "title")
}

// Restore godoc
// @Summary Restore a deleted deal (Admin/Sales Manager)
// @Description Restores a soft-deleted Deal.
// @Tags deals
// @Security BearerAuth
// @Produce json
// @Param id path int true "Deal ID"
// @Success 200 {object} models.Deal
// @Failure 404 {object} map[string]interface{} "Deleted deal not found"
// @Router /deals/{id}/restore [post]
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

// BulkReassign godoc
// @Summary Bulk reassign deals (Admin/Sales Manager)
// @Description Reassigns every listed Deal to assigned_to (or unassigns if null) in one transaction, writing a bulk_reassigned audit entry per row.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkReassignForm true "Deal IDs and new assignee"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "Invalid request body, or ids is required"
// @Router /deals/bulk-reassign [patch]
func (h *DealHandler) BulkReassign(c *fiber.Ctx) error {
	return bulkReassignEntity(c, h.DB, "deal",
		func(d *models.Deal) *uint { return d.AssignedTo },
		func(d *models.Deal, v *uint) { d.AssignedTo = v })
}

// BulkTag godoc
// @Summary Bulk tag deals (Admin/Sales Manager)
// @Description Applies tags to every listed Deal in one transaction (mode "add" merges, "set" replaces), writing a bulk_tagged audit entry per row.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkTagForm true "Deal IDs, tags, and mode (add/set)"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "Invalid request body, or ids is required"
// @Router /deals/bulk-tag [patch]
func (h *DealHandler) BulkTag(c *fiber.Ctx) error {
	return bulkTagEntity(c, h.DB, "deal",
		func(d *models.Deal) *uint { return d.AssignedTo },
		func(d *models.Deal) []string { return []string(d.Tags) },
		func(d *models.Deal, tags []string) { d.Tags = tags })
}

// BulkArchive godoc
// @Summary Bulk archive deals (Admin/Sales Manager)
// @Description Soft-deletes every listed Deal (same as Delete), in one transaction, writing a bulk_archived audit entry per row.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkIDsForm true "Deal IDs"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "Invalid request body, or ids is required"
// @Router /deals/bulk-archive [patch]
func (h *DealHandler) BulkArchive(c *fiber.Ctx) error {
	return bulkArchiveEntity(c, h.DB, "deal", func(d *models.Deal) *uint { return d.AssignedTo })
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
	// Position is the Kanban drag-drop's computed insertion point within the
	// destination Stage lane (a pointer so an omitted field, e.g. the mobile
	// dropdown-move, is distinguishable from an explicit 0) — see
	// Deal.Position's doc comment (models/deal.go).
	Position *float64 `json:"position"`
	// LostReason is optional here (the Kanban drag doesn't collect one), but
	// when sent on a move into a Lost stage it's validated and saved — the
	// Overview Pipeline's side panel asks for it (FR-CRM-123). Ignored on a
	// move into any other stage.
	LostReason *models.LostReason `json:"lost_reason"`
}

// UpdateStage godoc
// @Summary Move a deal to a new pipeline stage (Admin/Sales Rep/Sales Manager)
// @Description Dedicated endpoint for the Kanban drag-and-drop quick-move. Sets status to won/lost alongside stage — or open (clearing lost_reason) on a move into any other stage — and re-derives probability in the same transaction; writes a stage_changed audit log entry per §8.5's explicit minimum scope. Moving into a stage resolved as Won is blocked (422-style validation error) if AppSettings.RequireSignedContractBeforeWon is enabled and the deal has no Contract with status Signed. Only the assigned Sales Rep (or Admin/Sales Manager) may move it.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Deal ID"
// @Param body body dealStageForm true "New stage"
// @Success 200 {object} models.Deal
// @Header 200 {string} X-Lane-Rebalanced "\"true\" when the destination lane was renumbered to 1..n — refetch the lane, its other cards' positions changed"
// @Failure 400 {object} map[string]interface{} "Invalid request body, stage is required, stage is not a valid active pipeline stage, or a signed contract is required before marking this deal Won"
// @Failure 403 {object} map[string]interface{} "Not authorized to update this deal"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Failure 422 {object} map[string]interface{} "position out of range (±1e9)"
// @Router /deals/{id}/stage [patch]
func (h *DealHandler) UpdateStage(c *fiber.Ctx) error {
	var deal models.Deal
	if err := utils.FindByID(c, h.DB, &deal, "Deal not found"); err != nil {
		return nil
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
	to := utils.LookupStageFlags(h.DB, form.Stage)
	if !to.Active {
		return utils.ValidationError(c, "stage is not a valid active pipeline stage", map[string][]string{"stage": {"invalid"}})
	}
	if form.LostReason != nil && !models.IsValidLostReason(*form.LostReason) {
		return utils.ValidationError(c, "lost_reason is invalid", map[string][]string{"lost_reason": {"invalid"}})
	}
	if err := validateCardPosition(c, form.Position); err != nil {
		return nil
	}
	// Only on the transition into Won (deal.Status is still the stored
	// value), not on a reposition within the Won lane.
	if to.Won && deal.Status != models.DealStatusWon {
		if err := validateContractSignedBeforeWon(c, h.DB, deal.ID); err != nil {
			return nil
		}
	}

	before := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	oldStage := deal.Stage
	deal.Stage = form.Stage
	// A quick-move into an open stage always reopens, so it requests "open"
	// and where it came from doesn't matter.
	status, clearLostReason := resolveDealStatus(utils.StageFlags{}, to, false, models.DealStatusOpen)
	deal.Status = status
	switch {
	case to.Lost:
		// lost_reason is optional here; saved when sent with a move into Lost.
		if form.LostReason != nil {
			deal.LostReason = form.LostReason
		}
	case to.Won:
		// A move into Won leaves lost_reason as it was; only a reopen drops it.
		// Hook point: FR-CRM-064 auto-creates/updates a CustomerProduct(status: Active)
		// per Product on this Deal's accepted Quote — deferred until Quotes have a
		// real "accepted" flow to hang the side effect off.
	case clearLostReason:
		deal.LostReason = nil
	}
	// Re-derive probability for the new stage on every drag/quick-move (Kanban
	// has no probability input of its own) — the Deal's Overview tab can still
	// override it manually afterwards.
	if oldStage != deal.Stage {
		def := h.defaultProbabilityFor(deal.Stage)
		deal.Probability = &def
		catDef := h.defaultForecastCategoryFor(deal.Stage)
		deal.ForecastCategory = &catDef
		deal.MarkStageEntered(string(oldStage))
	}

	deal.Position = dealLanes.placeOnMove(h.DB, form.Position, oldStage != deal.Stage, deal.Stage, deal.Position)

	after := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	rebalanced := false
	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error {
		if err := tx.Save(&deal).Error; err != nil {
			return err
		}
		var err error
		if rebalanced, err = dealLanes.rebalanceIfCrowded(tx, deal.Stage, deal.ID, &deal.Position); err != nil {
			return err
		}
		if oldStage != deal.Stage {
			subject := fmt.Sprintf("Deal stage changed: %s → %s", oldStage, deal.Stage)
			return utils.LogCompanyActivity(tx, deal.CompanyID, subject, middleware.CurrentUserID(c))
		}
		return nil
	}, oldStage != deal.Stage, "deal", deal.ID, "stage_changed", before, after, middleware.CurrentUserID(c))
	if err != nil {
		return utils.Internal(c, "Failed to update deal stage")
	}
	if rebalanced {
		c.Set(LaneRebalancedHeader, "true")
	}
	return utils.OK(c, deal)
}

type dealReassignForm struct {
	AssignedTo *uint `json:"assigned_to"`
}

// Reassign godoc
// @Summary Reassign a deal (Admin/Sales Manager)
// @Description Sets the Deal's assigned_to, writing a reassigned audit log entry. Stricter than the deals group's default access — Sales Rep cannot call this.
// @Tags deals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Deal ID"
// @Param body body dealReassignForm true "New assignee"
// @Success 200 {object} models.Deal
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 404 {object} map[string]interface{} "Deal not found"
// @Failure 422 {object} map[string]interface{} "assigned_to is not an active sales-role user"
// @Router /deals/{id}/reassign [patch]
func (h *DealHandler) Reassign(c *fiber.Ctx) error {
	var deal models.Deal
	if err := utils.FindByID(c, h.DB, &deal, "Deal not found"); err != nil {
		return nil
	}

	var form dealReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := validateAssignee(h.DB, form.AssignedTo); err != nil {
		return respondAssigneeErr(c, err)
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

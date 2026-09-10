package handlers

import (
	"errors"
	"net/mail"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// validateExternalEmail rejects a syntactically invalid, non-empty email —
// unlike User accounts, a Lead's or Prospect's email belongs to an external
// contact so it isn't restricted to the company domain (see
// utils.IsValidCompanyEmail), just checked for basic format. Left
// unvalidated before, a garbage address would silently persist and then be
// relied on as an exact-match dedupe key by ImportHandler.ImportContacts.
// Named for what it validates (any external-contact email field), not which
// resource calls it — shared by LeadHandler and ProspectHandler.
//
// Returns utils.ErrHandled (see its doc) if invalid, nil if valid — NOT
// ValidationError's own return value, which is nil even on the invalid path
// since the JSON write itself succeeds; forwarding that would make the
// caller's `if err != nil` guard never fire.
func validateExternalEmail(c *fiber.Ctx, email string) error {
	if email == "" {
		return nil
	}
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		_ = utils.ValidationError(c, "email is not a valid address", map[string][]string{"email": {"invalid"}})
		return utils.ErrHandled
	}
	return nil
}

type LeadHandler struct {
	DB *gorm.DB
}

func NewLeadHandler(db *gorm.DB) *LeadHandler {
	return &LeadHandler{DB: db}
}

// List godoc
// @Summary List leads (Sales pipeline roles)
// @Description Paginated, filterable list of Leads. Admin/Sales Rep/Sales Manager only.
// @Tags leads
// @Security BearerAuth
// @Produce json
// @Param status query string false "Filter by lead status"
// @Param source query string false "Filter by lead source"
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID, or \"unassigned\""
// @Param company_id query int false "Filter by Company ID"
// @Param search query string false "Search by name, email, or company name"
// @Param sort query string false "Sort field, prefix with - for descending (e.g. -created_at, name)"
// @Param exclude_converted query bool false "Exclude leads already converted to a Deal"
// @Param page query int false "Page number (default 1)"
// @Param per_page query int false "Items per page (default 20, max 200)"
// @Success 200 {object} map[string]interface{} "Paginated lead list (data, page, per_page, total)"
// @Router /leads [get]
func (h *LeadHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Lead{})

	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("source"); v != "" {
		query = query.Where("source = ?", v)
	}
	if v := c.Query("assigned_to"); v == "unassigned" {
		query = query.Where("assigned_to IS NULL")
	} else if v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("leads.company_id = ?", v)
	}

	sortField := strings.TrimPrefix(c.Query("sort"), "-")
	search := c.Query("search")
	// The related Company's name is needed for either a "search" match or a
	// "company_name" sort — join once, up front, whenever either is in play
	// (utils.ApplyNullableCompanySearch), rather than duplicating the join
	// per use like deals.go/contacts.go's sort-only special case does (see
	// utils.ApplyCompanyNameSort, which those two share; Lead/Prospect can't
	// reuse that one since they also need the join for "search" and need a
	// LEFT JOIN rather than that helper's INNER JOIN — see its doc comment
	// for why).
	query, needsCompanyJoin := utils.ApplyNullableCompanySearch(query, "leads", sortField, search)
	if c.Query("exclude_converted") == "true" {
		query = query.Where("converted_deal_id IS NULL")
	}

	var total int64
	// Count() before the Select below — a plain COUNT(*) works fine against
	// the join as-is; it's only Find() that needs the column list narrowed
	// (see ApplyNullableCompanySort), and applying that narrowing here too
	// would break Count() against Postgres ("column leads.* does not exist").
	query.Count(&total)

	var leads []models.Lead
	if needsCompanyJoin {
		query = utils.ApplyNullableCompanySort(query, "leads", c.Query("sort"), sortField)
	} else {
		query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	}
	if err := query.Limit(perPage).Offset(offset).Find(&leads).Error; err != nil {
		return utils.Internal(c, "Failed to list leads")
	}
	return utils.List(c, leads, page, perPage, total)
}

type leadForm struct {
	Name       string            `json:"name"`
	CompanyID  *uint             `json:"company_id"`
	Email      string            `json:"email"`
	Phone      string            `json:"phone"`
	Source     models.LeadSource `json:"source"`
	Status     models.LeadStatus `json:"status"`
	Notes      string            `json:"notes"`
	AssignedTo *uint             `json:"assigned_to"`
	// Classification — FR-CRM-007. Only "sql" is honored as an explicit
	// manual override (a rep marking a Lead "sales-ready"); any other value
	// (including empty) falls back to the auto-computed mql/none result from
	// computeAndClassify, so a client can't accidentally set "mql" directly.
	Classification   models.LeadClassification `json:"classification"`
	BusinessUnit     *models.BusinessUnit      `json:"business_unit"`
	BusinessUnitItem *string                   `json:"business_unit_item"`
}

// Create godoc
// @Summary Create a lead (Sales pipeline roles)
// @Description Admin/Sales Rep/Sales Manager only. A Sales Rep cannot assign the new lead to another rep. If assigned_to is omitted, the lead is auto-assigned round-robin among active Sales Reps by current open-record load.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body leadForm true "Lead fields"
// @Success 201 {object} models.Lead
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 403 {object} map[string]interface{} "Cannot assign a lead to another sales rep"
// @Router /leads [post]
func (h *LeadHandler) Create(c *fiber.Ctx) error {
	var form leadForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a lead to another sales rep")
	}
	if !utils.IsActiveLeadSource(h.DB, string(form.Source)) {
		return utils.ValidationError(c, "source is not a valid active lead source", map[string][]string{"source": {"invalid"}})
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		return utils.ValidationError(c, "business_unit must be Project or Product", map[string][]string{"business_unit": {"invalid"}})
	}

	// Auto-assignment: only kicks in when the caller didn't specify an owner
	// (e.g. a brand-new Lead created without picking someone explicitly).
	// Explicit-assignee paths — Update, BulkReassign, Kanban drag, Convert —
	// never hit this because they always pass a concrete AssignedTo (or
	// intentionally leave it nil, which the same logic would fill in — but
	// today only Create is reachable with a nil AssignedTo from those flows).
	if form.AssignedTo == nil {
		if autoID, err := h.pickAutoAssignee(); err != nil {
			return utils.Internal(c, "Failed to auto-assign lead")
		} else if autoID != nil {
			form.AssignedTo = autoID
		}
	}

	lead := models.Lead{
		Name: form.Name, CompanyID: form.CompanyID, Email: form.Email, Phone: form.Phone,
		Source: form.Source, Status: form.Status, Notes: form.Notes, AssignedTo: form.AssignedTo,
		BusinessUnit: form.BusinessUnit, BusinessUnitItem: form.BusinessUnitItem,
	}
	if lead.Status == "" {
		lead.Status = models.LeadStatusNew
	}
	if err := h.computeAndClassify(&lead, form.Classification); err != nil {
		return utils.Internal(c, "Failed to score lead")
	}
	if err := h.DB.Create(&lead).Error; err != nil {
		return utils.Internal(c, "Failed to create lead")
	}
	return utils.Created(c, lead)
}

// computeLeadScore sums the Weight of every active LeadScoringCriterion that
// matches this Lead (FR-CRM-006). Unknown Field values never match — new
// match fields are additive, not something existing rows accidentally start
// matching. "has_company_name" keeps its original Field key (it's an
// Admin-configurable, already-seeded criterion row — renaming the key would
// silently stop matching for anyone's existing config) even though it now
// checks CompanyID rather than the free-text CompanyName it's named after.
func (h *LeadHandler) computeLeadScore(lead models.Lead) (int, error) {
	var criteria []models.LeadScoringCriterion
	if err := h.DB.Where("is_active = ?", true).Find(&criteria).Error; err != nil {
		return 0, err
	}
	score := 0
	for _, cr := range criteria {
		switch cr.Field {
		case "source":
			if string(lead.Source) == cr.MatchValue {
				score += cr.Weight
			}
		case "has_company_name":
			if lead.CompanyID != nil {
				score += cr.Weight
			}
		case "has_phone":
			if lead.Phone != "" {
				score += cr.Weight
			}
		}
	}
	return score, nil
}

// computeAndClassify recomputes lead.Score and sets lead.Classification —
// FR-CRM-006/007. manualClassification lets a caller explicitly mark a Lead
// "sql" (sales-ready); any other value defers to the auto mql/none result
// against AppSettings.LeadScoringMqlThreshold.
func (h *LeadHandler) computeAndClassify(lead *models.Lead, manualClassification models.LeadClassification) error {
	score, err := h.computeLeadScore(*lead)
	if err != nil {
		return err
	}
	lead.Score = score

	if manualClassification == models.LeadClassificationSQL {
		lead.Classification = string(models.LeadClassificationSQL)
		return nil
	}

	threshold := models.DefaultAppSettings.LeadScoringMqlThreshold
	var settings models.AppSettings
	if err := h.DB.First(&settings, 1).Error; err == nil {
		threshold = settings.LeadScoringMqlThreshold
	}
	if score >= threshold {
		lead.Classification = string(models.LeadClassificationMQL)
	} else {
		lead.Classification = string(models.LeadClassificationNone)
	}
	return nil
}

// pickAutoAssignee implements round-robin lead assignment via a stateless
// least-open-load strategy: among active Sales Reps, pick whoever currently
// owns the fewest open (non-closed) Leads+Deals. This is equivalent in
// steady-state to strict round robin (every assignment goes to whoever is
// "next up" by load) but needs no new cursor/counter table — it's derived
// live from existing assignment data, which also makes it self-healing if
// leads are reassigned/deleted/bulk-reassigned outside the rotation.
// Returns (nil, nil) if there are no active Sales Reps to assign to.
func (h *LeadHandler) pickAutoAssignee() (*uint, error) {
	var reps []models.User
	if err := h.DB.Where("role = ? AND is_active = ?", models.RoleSalesRep, true).
		Order("id").Find(&reps).Error; err != nil {
		return nil, err
	}
	if len(reps) == 0 {
		return nil, nil
	}

	type loadRow struct {
		UserID uint
		Cnt    int64
	}
	load := make(map[uint]int64, len(reps))
	for _, r := range reps {
		load[r.ID] = 0
	}

	var leadLoads []loadRow
	if err := h.DB.Model(&models.Lead{}).
		Select("assigned_to as user_id, count(*) as cnt").
		Where("assigned_to IS NOT NULL AND status NOT IN ?",
			[]models.LeadStatus{models.LeadStatusQualified, models.LeadStatusDisqualified}).
		Group("assigned_to").Scan(&leadLoads).Error; err != nil {
		return nil, err
	}
	for _, lr := range leadLoads {
		if _, ok := load[lr.UserID]; ok {
			load[lr.UserID] += lr.Cnt
		}
	}

	var dealLoads []loadRow
	if err := h.DB.Model(&models.Deal{}).
		Select("assigned_to as user_id, count(*) as cnt").
		Where("assigned_to IS NOT NULL AND status = ?", models.DealStatusOpen).
		Group("assigned_to").Scan(&dealLoads).Error; err != nil {
		return nil, err
	}
	for _, dr := range dealLoads {
		if _, ok := load[dr.UserID]; ok {
			load[dr.UserID] += dr.Cnt
		}
	}

	best := reps[0]
	bestLoad := load[best.ID]
	for _, r := range reps[1:] {
		if load[r.ID] < bestLoad {
			best, bestLoad = r, load[r.ID]
		}
	}
	id := best.ID
	return &id, nil
}

// Get godoc
// @Summary Get a lead
// @Description Any authenticated role (deliberately open — e.g. reachable from Prospect conversion).
// @Tags leads
// @Security BearerAuth
// @Produce json
// @Param id path int true "Lead ID"
// @Success 200 {object} models.Lead
// @Failure 404 {object} map[string]interface{} "Lead not found"
// @Router /leads/{id} [get]
func (h *LeadHandler) Get(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	return utils.OK(c, lead)
}

// Update godoc
// @Summary Update a lead (Sales pipeline roles)
// @Description Admin/Sales Rep/Sales Manager, and only if the caller owns the lead or has manager-level write access. Reassigning to another rep is likewise restricted. Omitting classification leaves an existing manual "sql" override in place rather than letting it be auto-recomputed away.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Lead ID"
// @Param body body leadForm true "Lead fields"
// @Success 200 {object} models.Lead
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 403 {object} map[string]interface{} "Not authorized to update this lead"
// @Failure 404 {object} map[string]interface{} "Lead not found"
// @Router /leads/{id} [put]
func (h *LeadHandler) Update(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this lead")
	}

	var form leadForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a lead to another sales rep")
	}
	if !utils.IsActiveLeadSource(h.DB, string(form.Source)) {
		return utils.ValidationError(c, "source is not a valid active lead source", map[string][]string{"source": {"invalid"}})
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		return utils.ValidationError(c, "business_unit must be Project or Product", map[string][]string{"business_unit": {"invalid"}})
	}

	lead.Name, lead.CompanyID, lead.Email, lead.Phone = form.Name, form.CompanyID, form.Email, form.Phone
	lead.Source, lead.Status, lead.Notes, lead.AssignedTo = form.Source, form.Status, form.Notes, form.AssignedTo
	lead.BusinessUnit, lead.BusinessUnitItem = form.BusinessUnit, form.BusinessUnitItem

	// A general-purpose Update PUT doesn't necessarily resend classification
	// (most fields, like a status/notes edit, have nothing to do with it), so
	// treat an omitted classification as "leave the manual sql override as it
	// was" rather than letting it fall through to computeAndClassify's
	// auto-recompute and silently downgrade a Lead a rep already marked
	// sales-ready. An explicit "sql" in the form still always wins.
	manualClassification := form.Classification
	if manualClassification == "" && models.LeadClassification(lead.Classification) == models.LeadClassificationSQL {
		manualClassification = models.LeadClassificationSQL
	}
	if err := h.computeAndClassify(&lead, manualClassification); err != nil {
		return utils.Internal(c, "Failed to score lead")
	}

	if err := h.DB.Save(&lead).Error; err != nil {
		return utils.Internal(c, "Failed to update lead")
	}
	return utils.OK(c, lead)
}

// Delete godoc
// @Summary Delete a lead (Sales pipeline roles)
// @Description Admin/Sales Rep/Sales Manager, and only if the caller owns the lead or has manager-level write access. Soft-delete — recoverable via Restore/Trash.
// @Tags leads
// @Security BearerAuth
// @Param id path int true "Lead ID"
// @Success 204 "No Content"
// @Failure 403 {object} map[string]interface{} "Not authorized to delete this lead"
// @Failure 404 {object} map[string]interface{} "Lead not found"
// @Router /leads/{id} [delete]
func (h *LeadHandler) Delete(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this lead")
	}
	actorID := middleware.CurrentUserID(c)
	if err := utils.GenericSoftDelete(h.DB, &lead, actorID); err != nil {
		return utils.Internal(c, "Failed to delete lead")
	}
	return utils.NoContent(c)
}

// Trash godoc
// @Summary List deleted leads (Admin/Sales Manager only)
// @Description Returns soft-deleted Leads.
// @Tags leads
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "Paginated lead list (data, page, per_page, total)"
// @Router /leads/trash [get]
func (h *LeadHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Lead](c, h.DB, "Failed to list deleted leads")
}

// Restore godoc
// @Summary Restore a deleted lead (Admin/Sales Manager only)
// @Description Un-does a soft-delete.
// @Tags leads
// @Security BearerAuth
// @Produce json
// @Param id path int true "Lead ID"
// @Success 200 {object} models.Lead
// @Failure 404 {object} map[string]interface{} "Deleted lead not found"
// @Router /leads/{id}/restore [post]
func (h *LeadHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Lead](c, h.DB, "Deleted lead not found", "Failed to restore lead")
}

// BulkReassign godoc
// @Summary Bulk-reassign leads (Admin/Sales Manager only)
// @Description Reassigns every listed Lead to a new owner in one transaction.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkReassignForm true "Lead IDs and new assignee"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "ids is required"
// @Router /leads/bulk-reassign [patch]
func (h *LeadHandler) BulkReassign(c *fiber.Ctx) error {
	var form bulkReassignForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "lead", "bulk_reassigned", actorID,
		func(tx *gorm.DB, lead *models.Lead) (models.JSONMap, models.JSONMap, error) {
			before := models.JSONMap{"assigned_to": lead.AssignedTo}
			lead.AssignedTo = form.AssignedTo
			after := models.JSONMap{"assigned_to": lead.AssignedTo}
			return before, after, tx.Save(lead).Error
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk reassign leads")
	}
	return utils.NoContent(c)
}

// BulkTag godoc
// @Summary Bulk-tag leads (Admin/Sales Manager only)
// @Description Adds ("add", default) or replaces ("set") tags on every listed Lead in one transaction.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkTagForm true "Lead IDs, tags, and mode (add/set)"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "ids is required"
// @Router /leads/bulk-tag [patch]
func (h *LeadHandler) BulkTag(c *fiber.Ctx) error {
	var form bulkTagForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "lead", "bulk_tagged", actorID,
		func(tx *gorm.DB, lead *models.Lead) (models.JSONMap, models.JSONMap, error) {
			before := models.JSONMap{"tags": []string(lead.Tags)}
			if form.Mode == "set" {
				lead.Tags = form.Tags
			} else {
				lead.Tags = mergeTags(lead.Tags, form.Tags)
			}
			after := models.JSONMap{"tags": []string(lead.Tags)}
			return before, after, tx.Save(lead).Error
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk tag leads")
	}
	return utils.NoContent(c)
}

// BulkArchive godoc
// @Summary Bulk-archive leads (Admin/Sales Manager only)
// @Description Soft-deletes every listed Lead (same as Delete), in one transaction.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkIDsForm true "Lead IDs"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "ids is required"
// @Router /leads/bulk-archive [patch]
func (h *LeadHandler) BulkArchive(c *fiber.Ctx) error {
	var form bulkIDsForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if len(form.IDs) == 0 {
		return utils.ValidationError(c, "ids is required", map[string][]string{"ids": {"required"}})
	}

	actorID := middleware.CurrentUserID(c)
	err := utils.BulkUpdate(h.DB, form.IDs, "lead", "bulk_archived", actorID,
		func(tx *gorm.DB, lead *models.Lead) (models.JSONMap, models.JSONMap, error) {
			if err := tx.Model(lead).Update("deleted_by", actorID).Error; err != nil {
				return nil, nil, err
			}
			err := tx.Delete(lead).Error
			return models.JSONMap{"deleted_at": nil}, models.JSONMap{"deleted_by": actorID}, err
		})
	if err != nil {
		return utils.Internal(c, "Failed to bulk archive leads")
	}
	return utils.NoContent(c)
}

type convertRequest struct {
	CompanyID *uint `json:"company_id"`
	ContactID *uint `json:"contact_id"`
	Deal      struct {
		Title             string               `json:"title"`
		Value             float64              `json:"value"`
		Stage             models.DealStage     `json:"stage"`
		ExpectedCloseDate *string              `json:"expected_close_date"`
		AssignedTo        *uint                `json:"assigned_to"`
		Channel           models.LeadSource    `json:"channel"`
		BusinessUnit      *models.BusinessUnit `json:"business_unit"`
		BusinessUnitItem  *string              `json:"business_unit_item"`
	} `json:"deal"`
}

// Convert godoc
// @Summary Convert a lead to a deal (Sales pipeline roles)
// @Description Admin/Sales Rep/Sales Manager, and only if the caller owns the lead or has manager-level write access. Converts a Lead into a Deal, reusing or creating the linked Company/Contact as needed — FR-CRM-004, api-system-spec.md §3. Fails if the lead was already converted.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Lead ID"
// @Param body body convertRequest true "Optional company_id/contact_id overrides and the new deal's fields"
// @Success 200 {object} map[string]interface{} "deal, company, and contact objects"
// @Failure 400 {object} map[string]interface{} "Invalid body, or stage/channel not a valid active value"
// @Failure 403 {object} map[string]interface{} "Not authorized to convert this lead"
// @Failure 404 {object} map[string]interface{} "Lead not found"
// @Failure 409 {object} map[string]interface{} "Lead has already been converted"
// @Router /leads/{id}/convert [post]
func (h *LeadHandler) Convert(c *fiber.Ctx) error {
	var lead models.Lead
	if err := h.DB.First(&lead, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Lead not found")
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to convert this lead")
	}
	if lead.ConvertedDealID != nil {
		return utils.Conflict(c, "Lead has already been converted")
	}

	var req convertRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.IsActivePipelineStage(h.DB, string(req.Deal.Stage)) {
		return utils.ValidationError(c, "stage is not a valid active pipeline stage", map[string][]string{"stage": {"invalid"}})
	}
	if !utils.IsActiveLeadSource(h.DB, string(req.Deal.Channel)) {
		return utils.ValidationError(c, "channel is not a valid active lead source", map[string][]string{"channel": {"invalid"}})
	}

	var company models.Company
	var contact models.Contact
	var deal models.Deal

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		switch {
		case req.CompanyID != nil:
			// Explicit override from the Convert form always wins, even if
			// it differs from whatever Company the Lead itself was already
			// linked to.
			if err := tx.First(&company, *req.CompanyID).Error; err != nil {
				return err
			}
		case lead.CompanyID != nil:
			// The normal case since 2026-08-24: the Lead was already linked
			// to a real Company (via the create/edit combobox), so reuse it
			// directly instead of creating a fresh duplicate from its name —
			// closes the dedupe gap this convert path used to have. Unlike
			// an explicit req.CompanyID above (a caller mistake, worth
			// failing loudly on), this id was never caller-supplied on this
			// request — if the Company it points to has since been
			// soft-deleted, fall back to creating a fresh one rather than
			// failing the whole conversion over a Company the rep never
			// chose here in the first place.
			if err := tx.First(&company, *lead.CompanyID).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				company = models.Company{Status: models.StatusActive}
				if err := tx.Create(&company).Error; err != nil {
					return err
				}
			}
		default:
			// Rare going forward (every new Lead picks/creates a Company via
			// the frontend combobox), but a Lead can still reach here with no
			// Company at all — preserves the old fallback behavior rather
			// than newly requiring one at convert time.
			company = models.Company{Status: models.StatusActive}
			if err := tx.Create(&company).Error; err != nil {
				return err
			}
		}

		if req.ContactID != nil {
			if err := tx.First(&contact, *req.ContactID).Error; err != nil {
				return err
			}
		} else {
			contact = models.Contact{
				CompanyID: company.ID, Name: lead.Name, Email: lead.Email, Phone: lead.Phone,
				Status: models.StatusActive,
			}
			if err := tx.Create(&contact).Error; err != nil {
				return err
			}
		}

		deal = models.Deal{
			CompanyID: company.ID, ContactID: contact.ID,
			Title: req.Deal.Title, Value: req.Deal.Value, Stage: req.Deal.Stage,
			Status: models.DealStatusOpen, ExpectedCloseDate: req.Deal.ExpectedCloseDate,
			AssignedTo: req.Deal.AssignedTo, Channel: req.Deal.Channel,
			BusinessUnit: req.Deal.BusinessUnit, BusinessUnitItem: req.Deal.BusinessUnitItem,
			LeadID: &lead.ID,
		}
		if deal.Title == "" {
			deal.Title = lead.Name
		}
		if deal.Stage == "" {
			deal.Stage = models.DealStageQualified
		}
		def := models.StageDefaultProbability(deal.Stage)
		deal.Probability = &def
		if err := tx.Create(&deal).Error; err != nil {
			return err
		}

		// FR-CRM-090: carry any Lead attachments over to the new Deal rather
		// than leaving them stranded on a Lead that no longer appears in any
		// list view once converted.
		if err := tx.Model(&models.Attachment{}).
			Where("related_type = ? AND related_id = ?", models.AttachmentRelatedLead, lead.ID).
			Updates(map[string]interface{}{"related_type": models.AttachmentRelatedDeal, "related_id": deal.ID}).Error; err != nil {
			return err
		}

		lead.Status = models.LeadStatusQualified
		lead.ConvertedDealID = &deal.ID
		return tx.Save(&lead).Error
	})
	if err != nil {
		return utils.Internal(c, "Failed to convert lead")
	}

	return utils.OK(c, fiber.Map{"deal": deal, "company": company, "contact": contact})
}

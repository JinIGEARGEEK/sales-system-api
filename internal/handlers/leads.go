package handlers

import (
	"net/mail"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// validateExternalEmail rejects a syntactically invalid, non-empty email on
// a Lead or Prospect. It's an external contact's address, so unlike a
// User's (utils.IsValidCompanyEmail) any domain is fine; the format still
// matters because ImportHandler.ImportContacts dedupes on it.
//
// Returns utils.ErrHandled (see its doc) if invalid, nil if valid.
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
// @Param only_converted query bool false "Only leads already converted to a Deal"
// @Param page query int false "Page number (default 1)"
// @Param per_page query int false "Items per page (default 20, max 200)"
// @Success 200 {object} map[string]interface{} "Paginated lead list (data, page, per_page, total)"
// @Router /leads [get]
func (h *LeadHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Lead{})

	query, needsCompanyJoin, sortField := applyLeadLikeFilters(query, c, "leads", "converted_deal_id")

	var total int64
	query.Count(&total)

	var leads []models.Lead
	query = applyLeadLikeSort(query, c, "leads", needsCompanyJoin, sortField)
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
	Classification   models.LeadClassification   `json:"classification"`
	BusinessUnit     *models.BusinessUnit        `json:"business_unit"`
	BusinessUnitItem *string                     `json:"business_unit_item"`
	ReferredByType   *models.ActivityRelatedType `json:"referred_by_type"`
	ReferredByID     *uint                       `json:"referred_by_id"`
}

// validateReferredBy enforces both-or-neither on ReferredByType/ReferredByID
// — a Lead's referrer is always an existing Company or Contact, never a
// Deal/Prospect, per models.IsValidReferrerType (activity.go, next to
// ActivityRelatedType's own definition — the broader enum this borrows from)
// — and, once type is known, that the referenced row actually exists.
func validateReferredBy(c *fiber.Ctx, db *gorm.DB, referredByType *models.ActivityRelatedType, referredByID *uint) error {
	if referredByType == nil && referredByID == nil {
		return nil
	}
	if referredByType == nil || referredByID == nil {
		_ = utils.ValidationError(c, "referred_by_type and referred_by_id must both be set or both omitted", map[string][]string{"referred_by_type": {"required_with_referred_by_id"}})
		return utils.ErrHandled
	}
	if !models.IsValidReferrerType(*referredByType) {
		_ = utils.ValidationError(c, "referred_by_type must be company or contact", map[string][]string{"referred_by_type": {"invalid"}})
		return utils.ErrHandled
	}
	var existsErr error
	if *referredByType == models.RelatedTypeCompany {
		existsErr = db.First(&models.Company{}, *referredByID).Error
	} else {
		existsErr = db.First(&models.Contact{}, *referredByID).Error
	}
	if existsErr != nil {
		_ = utils.NotFound(c, "Referred-by company/contact not found")
		return utils.ErrHandled
	}
	return nil
}

// validateLeadCompanyID checks that an explicitly-set (optional) company_id
// actually exists — unlike Deal/Contact's own company_id, which are
// presence-checked (required) but never existence-checked against the DB,
// this is the one real "does this FK exist" precedent in the codebase,
// mirroring projects.go's own Company lookup.
func validateLeadCompanyID(c *fiber.Ctx, db *gorm.DB, companyID *uint) error {
	if companyID == nil {
		return nil
	}
	if err := db.First(&models.Company{}, *companyID).Error; err != nil {
		_ = utils.NotFound(c, "Company not found")
		return utils.ErrHandled
	}
	return nil
}

// validateLeadStatus writes a 422 and returns utils.ErrHandled for a status
// outside models.ValidLeadStatuses. Empty is allowed through; each caller
// decides what it means (Create: New, Update: keep, UpdateStatus: required).
func validateLeadStatus(c *fiber.Ctx, status models.LeadStatus) error {
	if status == "" || models.IsValidLeadStatus(status) {
		return nil
	}
	_ = utils.ValidationError(c, "status must be New, Contacted, Qualified or Disqualified", map[string][]string{"status": {"invalid"}})
	return utils.ErrHandled
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
// @Failure 422 {object} map[string]interface{} "status is not New/Contacted/Qualified/Disqualified"
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
	if err := validateLeadStatus(c, form.Status); err != nil {
		return nil
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		return utils.ValidationError(c, "business_unit must be Project or Product", map[string][]string{"business_unit": {"invalid"}})
	}
	if err := validateReferredBy(c, h.DB, form.ReferredByType, form.ReferredByID); err != nil {
		return nil
	}
	if err := validateLeadCompanyID(c, h.DB, form.CompanyID); err != nil {
		return nil
	}

	// Auto-assign only when the caller picked no owner; other write paths
	// never auto-assign.
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
		ReferredByType: form.ReferredByType, ReferredByID: form.ReferredByID,
	}
	if lead.Status == "" {
		lead.Status = models.LeadStatusNew
	}
	if err := h.computeAndClassify(&lead, form.Classification); err != nil {
		return utils.Internal(c, "Failed to score lead")
	}
	lead.Position = leadLanes.next(h.DB, lead.Status)
	if err := h.DB.Create(&lead).Error; err != nil {
		return utils.Internal(c, "Failed to create lead")
	}
	return utils.Created(c, lead)
}

// computeLeadScore sums the Weight of every active LeadScoringCriterion that
// matches this Lead (FR-CRM-006). Unknown Field values never match — new
// match fields are additive, not something existing rows accidentally start
// matching. "has_company_name" checks CompanyID; the key keeps its name so
// existing Admin-configured criteria keep matching.
func (h *LeadHandler) computeLeadScore(lead models.Lead) (int, error) {
	score, _, err := h.computeLeadScoreDetailed(lead)
	return score, err
}

// computeLeadScoreDetailed is computeLeadScore plus which criteria matched,
// for FR-CRM-007's score breakdown (GET /leads/:id/score-breakdown).
func (h *LeadHandler) computeLeadScoreDetailed(lead models.Lead) (int, []models.LeadScoringCriterion, error) {
	var criteria []models.LeadScoringCriterion
	if err := h.DB.Where("is_active = ?", true).Find(&criteria).Error; err != nil {
		return 0, nil, err
	}
	score := 0
	matched := make([]models.LeadScoringCriterion, 0, len(criteria))
	for _, cr := range criteria {
		isMatch := false
		switch cr.Field {
		case "source":
			isMatch = string(lead.Source) == cr.MatchValue
		case "has_company_name":
			isMatch = lead.CompanyID != nil
		case "has_phone":
			isMatch = lead.Phone != ""
		}
		if isMatch {
			score += cr.Weight
			matched = append(matched, cr)
		}
	}
	return score, matched, nil
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
	if err := utils.FindByID(c, h.DB, &lead, "Lead not found"); err != nil {
		return nil
	}
	return utils.OK(c, lead)
}

type scoreBreakdownCriterion struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Field  string `json:"field"`
	Weight int    `json:"weight"`
}

// ScoreBreakdown godoc
// @Summary Explain a Lead's score (Sales pipeline roles)
// @Description FR-CRM-007 — returns the same total as Lead.Score plus which active LeadScoringCriterion rows matched and contributed, so a rep can see why a Lead scored what it did without needing Admin access to /admin/lead-scoring-criteria (Admin-only). Recomputed live from the Lead's current fields, same as computeAndClassify — always consistent with Lead.Score even if criteria changed since the Lead was last saved.
// @Tags leads
// @Security BearerAuth
// @Produce json
// @Param id path int true "Lead ID"
// @Success 200 {object} map[string]interface{} "{score, threshold, classification, matched: scoreBreakdownCriterion[]}"
// @Failure 404 {object} map[string]interface{} "Lead not found"
// @Router /leads/{id}/score-breakdown [get]
func (h *LeadHandler) ScoreBreakdown(c *fiber.Ctx) error {
	var lead models.Lead
	if err := utils.FindByID(c, h.DB, &lead, "Lead not found"); err != nil {
		return nil
	}

	score, matchedCriteria, err := h.computeLeadScoreDetailed(lead)
	if err != nil {
		return utils.Internal(c, "Failed to compute score breakdown")
	}

	threshold := models.DefaultAppSettings.LeadScoringMqlThreshold
	var settings models.AppSettings
	if err := h.DB.First(&settings, 1).Error; err == nil {
		threshold = settings.LeadScoringMqlThreshold
	}

	matched := make([]scoreBreakdownCriterion, 0, len(matchedCriteria))
	for _, cr := range matchedCriteria {
		matched = append(matched, scoreBreakdownCriterion{ID: cr.ID, Name: cr.Name, Field: cr.Field, Weight: cr.Weight})
	}

	return utils.OK(c, fiber.Map{
		"score":          score,
		"threshold":      threshold,
		"classification": lead.Classification,
		"matched":        matched,
	})
}

// Update godoc
// @Summary Update a lead (Sales pipeline roles)
// @Description Admin/Sales Rep/Sales Manager, and only if the caller owns the lead or has manager-level write access. Reassigning to another rep is likewise restricted. Omitting classification leaves an existing manual "sql" override in place rather than letting it be auto-recomputed away; omitting status keeps the stored one.
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
// @Failure 422 {object} map[string]interface{} "status is not New/Contacted/Qualified/Disqualified"
// @Router /leads/{id} [put]
func (h *LeadHandler) Update(c *fiber.Ctx) error {
	var lead models.Lead
	if err := utils.FindByID(c, h.DB, &lead, "Lead not found"); err != nil {
		return nil
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
	if err := validateLeadStatus(c, form.Status); err != nil {
		return nil
	}
	if err := validateExternalEmail(c, form.Email); err != nil {
		return nil
	}
	if !models.IsValidBusinessUnit(form.BusinessUnit) {
		return utils.ValidationError(c, "business_unit must be Project or Product", map[string][]string{"business_unit": {"invalid"}})
	}
	if err := validateReferredBy(c, h.DB, form.ReferredByType, form.ReferredByID); err != nil {
		return nil
	}
	if err := validateLeadCompanyID(c, h.DB, form.CompanyID); err != nil {
		return nil
	}

	// Captured before the mutation: the form resubmits the full Lead on
	// every save, so this is how a real status change is told apart.
	oldStatus := lead.Status
	oldCompanyID := lead.CompanyID
	if form.Status == "" {
		form.Status = lead.Status // omitted keeps the stored status, not ""
	}

	lead.Name, lead.CompanyID, lead.Email, lead.Phone = form.Name, form.CompanyID, form.Email, form.Phone
	lead.Source, lead.Status, lead.Notes, lead.AssignedTo = form.Source, form.Status, form.Notes, form.AssignedTo
	lead.BusinessUnit, lead.BusinessUnitItem = form.BusinessUnit, form.BusinessUnitItem
	lead.ReferredByType, lead.ReferredByID = form.ReferredByType, form.ReferredByID
	if oldStatus != lead.Status {
		lead.MarkStageEntered(string(oldStatus))
		// No drag geometry on the edit form — append to the new lane's end.
		lead.Position = leadLanes.next(h.DB, lead.Status)
	}

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

	// Logs a company-scoped Activity when status actually changed, so
	// Company.last_activity_at reflects that the customer was contacted —
	// mirrors ProspectHandler.Update/DealHandler.Update's same treatment.
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&lead).Error; err != nil {
			return err
		}
		return utils.LogStatusChangeActivity(tx, "Lead", oldStatus, lead.Status, lead.CompanyID, oldCompanyID, middleware.CurrentUserID(c))
	})
	if err != nil {
		return utils.Internal(c, "Failed to update lead")
	}
	return utils.OK(c, lead)
}

type leadStatusForm struct {
	Status models.LeadStatus `json:"status"`
	// Position is the Kanban drag-drop's computed insertion point within the
	// destination Status lane (a pointer so an omitted field, e.g. the mobile
	// dropdown-move, is distinguishable from an explicit 0) — see
	// Lead.Position's doc comment (models/lead.go).
	Position *float64 `json:"position"`
}

// UpdateStatus godoc
// @Summary Move a lead to a new status (Kanban drag-and-drop) (Sales pipeline roles)
// @Description Dedicated narrow-PATCH endpoint for the Kanban drag-and-drop quick-move — unlike the full-record `PUT /leads/:id`, this only ever touches status/position, so a drag-move never risks blanking out the rest of the record. Mirrors DealHandler.UpdateStage.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Lead ID"
// @Param body body leadStatusForm true "New status/position"
// @Success 200 {object} models.Lead
// @Header 200 {string} X-Lane-Rebalanced "\"true\" when the destination lane was renumbered to 1..n — refetch the lane, its other cards' positions changed"
// @Failure 400 {object} map[string]interface{} "status is required"
// @Failure 403 {object} map[string]interface{} "Not authorized to update this lead"
// @Failure 404 {object} map[string]interface{} "Lead not found"
// @Failure 422 {object} map[string]interface{} "status is not New/Contacted/Qualified/Disqualified, or position out of range (±1e9)"
// @Router /leads/{id}/status [patch]
func (h *LeadHandler) UpdateStatus(c *fiber.Ctx) error {
	var lead models.Lead
	if err := utils.FindByID(c, h.DB, &lead, "Lead not found"); err != nil {
		return nil
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to update this lead")
	}

	var form leadStatusForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Status == "" {
		return utils.ValidationError(c, "status is required", map[string][]string{"status": {"required"}})
	}
	if err := validateLeadStatus(c, form.Status); err != nil {
		return nil
	}
	if err := validateCardPosition(c, form.Position); err != nil {
		return nil
	}

	oldStatus := lead.Status
	lead.Status = form.Status
	lead.Position = leadLanes.placeOnMove(h.DB, form.Position, oldStatus != lead.Status, lead.Status, lead.Position)
	if oldStatus != lead.Status {
		lead.MarkStageEntered(string(oldStatus))
	}

	rebalanced := false
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&lead).Error; err != nil {
			return err
		}
		var err error
		if rebalanced, err = leadLanes.rebalanceIfCrowded(tx, lead.Status, lead.ID, &lead.Position); err != nil {
			return err
		}
		return utils.LogStatusChangeActivity(tx, "Lead", oldStatus, lead.Status, lead.CompanyID, lead.CompanyID, middleware.CurrentUserID(c))
	})
	if err != nil {
		return utils.Internal(c, "Failed to update lead status")
	}
	if rebalanced {
		c.Set(LaneRebalancedHeader, "true")
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
	if err := utils.FindByID(c, h.DB, &lead, "Lead not found"); err != nil {
		return nil
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
// @Param search query string false "Search by name"
// @Success 200 {object} map[string]interface{} "Paginated lead list (data, page, per_page, total)"
// @Router /leads/trash [get]
func (h *LeadHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Lead](c, h.DB, "Failed to list deleted leads", "name")
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
	return bulkReassignEntity(c, h.DB, "lead",
		func(l *models.Lead) *uint { return l.AssignedTo },
		func(l *models.Lead, v *uint) { l.AssignedTo = v })
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
	return bulkTagEntity(c, h.DB, "lead",
		func(l *models.Lead) *uint { return l.AssignedTo },
		func(l *models.Lead) []string { return []string(l.Tags) },
		func(l *models.Lead, tags []string) { l.Tags = tags })
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
	return bulkArchiveEntity(c, h.DB, "lead", func(l *models.Lead) *uint { return l.AssignedTo })
}

type convertRequest struct {
	CompanyID *uint      `json:"company_id"`
	ContactID *uint      `json:"contact_id"`
	Deal      dealFields `json:"deal"`
}

// Convert godoc
// @Summary Convert a lead to a deal (Sales pipeline roles)
// @Description Admin/Sales Rep/Sales Manager, and only if the caller owns the lead or has manager-level write access. Converts a Lead into a Deal, reusing or creating the linked Company/Contact as needed — FR-CRM-004, api-system-spec.md §3. The new deal gets Deal Create's validation: value >= 0, a valid expected_close_date, an active stage/channel, a valid business_unit, lost_reason on a Lost stage, the signed-contract gate on a Won stage, and status following the stage's Won/Lost flags. A Sales Rep cannot assign the deal to another rep. An explicit company_id/contact_id must exist, and the contact must belong to that company. Fails if the lead was already converted, including by a concurrent request.
// @Tags leads
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Lead ID"
// @Param body body convertRequest true "Optional company_id/contact_id overrides and the new deal's fields"
// @Success 200 {object} map[string]interface{} "deal, company, and contact objects"
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 403 {object} map[string]interface{} "Not authorized to convert this lead, or cannot assign a deal to another sales rep"
// @Failure 404 {object} map[string]interface{} "Lead, company or contact not found"
// @Failure 409 {object} map[string]interface{} "Lead has already been converted"
// @Failure 422 {object} map[string]interface{} "Validation error (value, expected_close_date, stage/channel/business_unit, lost_reason, signed contract required, contact not in company)"
// @Router /leads/{id}/convert [post]
func (h *LeadHandler) Convert(c *fiber.Ctx) error {
	var lead models.Lead
	if err := utils.FindByID(c, h.DB, &lead, "Lead not found"); err != nil {
		return nil
	}
	if !CanWrite(c, lead.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to convert this lead")
	}
	// Fast path only — the authoritative check is the locked re-read inside
	// the transaction below.
	if lead.ConvertedDealID != nil {
		return utils.Conflict(c, "Lead has already been converted")
	}

	var req convertRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if req.Deal.Stage == "" {
		// "Qualified" by default; the first open stage if an Admin has
		// renamed or retired it. Resolved before validation so the Won/Lost
		// gates below see the stage the deal will actually get.
		req.Deal.Stage = models.DealStageQualified
		if !utils.IsActivePipelineStage(h.DB, string(req.Deal.Stage)) {
			req.Deal.Stage = utils.DefaultPipelineStage(h.DB)
		}
	}

	// Deal Create's checks, so a converted deal can't skip any of them.
	form := dealForm{dealFields: req.Deal}
	to, err := validateNewDealForm(c, h.DB, form)
	if err != nil {
		return nil
	}

	var company models.Company
	var contact models.Contact
	var deal models.Deal

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		// Locked re-read + re-check: two concurrent Converts both passed the
		// pre-check above; the second waits here and then sees the first's
		// ConvertedDealID instead of creating a second Deal.
		if err := lockForConvert(tx, &lead, lead.ID); err != nil {
			return err
		}
		if lead.ConvertedDealID != nil {
			return errAlreadyConverted
		}

		// An explicit req.CompanyID wins over the Lead's own company (the
		// usual case) — see resolveOrCreateCompany.
		var err error
		company, err = resolveOrCreateCompany(tx, req.CompanyID, lead.CompanyID)
		if err != nil {
			return err
		}
		contact, err = resolveOrCreateContact(tx, req.ContactID, company.ID, lead.Name, lead.Email, lead.Phone)
		if err != nil {
			return err
		}

		status, clearLostReason := resolveDealStatus(to, to, false, models.DealStatusOpen)
		deal = models.Deal{
			CompanyID: company.ID, ContactID: contact.ID,
			Title: form.Title, Value: form.Value, Stage: form.Stage,
			Status: status, ExpectedCloseDate: form.ExpectedCloseDate,
			AssignedTo: form.AssignedTo, Channel: form.Channel,
			BusinessUnit: form.BusinessUnit, BusinessUnitItem: form.BusinessUnitItem,
			LeadID: &lead.ID,
		}
		if deal.Title == "" {
			deal.Title = lead.Name
		}
		if !clearLostReason {
			deal.LostReason = form.LostReason
		}
		// Same defaults as Deal Create: the stage's configured probability
		// (so a renamed stage keeps its number) and its forecast category.
		prob := utils.StageDefaultProbability(tx, deal.Stage)
		category := models.StageDefaultForecastCategory(deal.Stage)
		deal.Probability, deal.ForecastCategory = &prob, &category
		deal.Position = dealLanes.next(tx, deal.Stage)
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

		// Always restamped, even if already Qualified: on the Overview
		// Pipeline a converted Lead moves into its own Converted lane
		// (FR-CRM-123), so conversion is a lane change there.
		lead.MarkStageEntered(string(lead.Status))
		lead.Status = models.LeadStatusQualified
		lead.Position = leadLanes.next(tx, lead.Status)
		lead.ConvertedDealID = &deal.ID
		if err := tx.Save(&lead).Error; err != nil {
			return err
		}
		return utils.LogCompanyActivity(tx, company.ID, "Lead converted to Deal", middleware.CurrentUserID(c))
	})
	if err != nil {
		if writeConvertTxError(c, err, "Lead has already been converted") {
			return nil
		}
		return utils.Internal(c, "Failed to convert lead")
	}

	return utils.OK(c, fiber.Map{"deal": deal, "company": company, "contact": contact})
}

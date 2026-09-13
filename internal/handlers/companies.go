package handlers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// companyWithActivity embeds Company plus the last_activity_at computed by
// withLastActivityAt's join (company_activity.go) — null/omitted when the
// Company has no company-scoped Activities at all. Used by both List and Get
// so the field's shape is identical everywhere it's returned.
type companyWithActivity struct {
	models.Company
	LastActivityAt *time.Time `json:"last_activity_at"`
}

// normalizeActiveArchivedStatus trims/lowercases the given status so callers
// aren't silently tripped up by casing or whitespace (e.g. "Active",
// " active "). Empty input is valid (caller decides the default); anything
// else must match one of the canonical ActiveArchivedStatus values. Shared by
// CompanyHandler and ContactHandler, which both use this same status enum.
func normalizeActiveArchivedStatus(v string) (models.ActiveArchivedStatus, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", true
	}
	status := models.ActiveArchivedStatus(v)
	if status != models.StatusActive && status != models.StatusArchived {
		return "", false
	}
	return status, true
}

// normalizeTags trims/lowercases each tag and drops empty/duplicate entries,
// preserving first-seen order — so "VIP" and "vip" sent on different calls
// end up as the same stored tag instead of silently accumulating
// near-duplicates that ?tag= filtering would then have to work around.
// Shared by CompanyHandler and ContactHandler, which both store Tags the
// same way (pq.StringArray).
func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// conflictingCompanyDomain looks up an existing, different Company whose
// Domain (derived from Website) matches domain — used by Create/Update to
// reject an obvious duplicate with a friendly, specific error rather than a
// generic 500 once the unique index below rejects the write. This CRM is
// meant to be the source of truth other internal systems sync Company data
// through, so a second row for the same real-world company isn't just
// clutter, it's a data-integrity problem for everything downstream reading
// from it. excludeID is the company being updated (0 on Create, so nothing
// is excluded). Returns nil, nil when domain is empty or no match exists.
//
// This is a fast-path pre-check, not the actual guarantee: two concurrent
// Creates for the same domain could both pass it before either commits. The
// real guarantee is the partial unique index on companies(domain) (see
// database.go's AutoMigrate) — Create/Update below re-run this same lookup
// if the write itself fails, to turn that race's constraint violation into
// the same friendly 409 instead of a generic 500.
func conflictingCompanyDomain(db *gorm.DB, domain string, excludeID uint) (*models.Company, error) {
	if domain == "" {
		return nil, nil
	}
	var existing models.Company
	err := db.Where("domain = ? AND id <> ?", domain, excludeID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

// companyDomainConflictMessage formats conflictingCompanyDomain's match into
// the 409 body's message — shared by the pre-check and the post-write race
// fallback so both report the conflict identically.
func companyDomainConflictMessage(dup *models.Company, excludeID uint) string {
	verb := "A company"
	if excludeID != 0 {
		verb = "A different company"
	}
	return fmt.Sprintf("%s with this website already exists (id %d, %q)", verb, dup.ID, dup.Name)
}

// companyFormResult carries the values validateCompanyForm derives that
// Create/Update both need afterward — computed once here rather than twice
// (Website's Domain, and Status normalized/defaulted) — since both handlers
// otherwise ran the identical derivation a second time.
type companyFormResult struct {
	Domain string
	Status models.ActiveArchivedStatus
}

// validateCompanyForm runs every check CompanyHandler.Create and Update
// share — required name, website format, industry registration, active
// size/revenue_size, status normalization, and domain-conflict dedupe.
// excludeID is the Company being updated (0 on Create, so nothing is
// excluded from the dedupe check).
//
// Returns utils.ErrHandled (see its doc) if invalid — the caller should
// `return nil`, not `return err`, exactly like every other validateX helper
// in this codebase (see validateDealRequiredFields, deals.go).
func validateCompanyForm(c *fiber.Ctx, db *gorm.DB, form companyForm, excludeID uint) (companyFormResult, error) {
	if form.Name == "" {
		_ = utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
		return companyFormResult{}, utils.ErrHandled
	}
	// Extracted once here (rather than via utils.IsValidWebsite, which would
	// re-derive it internally) since Create/Update need the same Domain value
	// afterward regardless of whether it's valid.
	domain := utils.ExtractDomain(form.Website)
	if form.Website != "" && !utils.IsValidWebsiteDomain(domain) {
		_ = utils.ValidationError(c, "website is not a valid domain/URL", map[string][]string{"website": {"invalid"}})
		return companyFormResult{}, utils.ErrHandled
	}
	if err := utils.EnsureActiveIndustry(db, form.Industry); err != nil {
		_ = utils.Internal(c, "Failed to save industry option")
		return companyFormResult{}, utils.ErrHandled
	}
	if !utils.IsActiveCompanySize(db, form.Size) {
		_ = utils.ValidationError(c, "size is not a valid active company size", map[string][]string{"size": {"invalid"}})
		return companyFormResult{}, utils.ErrHandled
	}
	if !utils.IsActiveRevenueSize(db, form.RevenueSize) {
		_ = utils.ValidationError(c, "revenue_size is not a valid active revenue size", map[string][]string{"revenue_size": {"invalid"}})
		return companyFormResult{}, utils.ErrHandled
	}
	status, ok := normalizeActiveArchivedStatus(form.Status)
	if !ok {
		_ = utils.ValidationError(c, "status must be active or archived", map[string][]string{"status": {"invalid"}})
		return companyFormResult{}, utils.ErrHandled
	}
	if dup, err := conflictingCompanyDomain(db, domain, excludeID); err != nil {
		_ = utils.Internal(c, "Failed to check for an existing company")
		return companyFormResult{}, utils.ErrHandled
	} else if dup != nil {
		_ = utils.Conflict(c, companyDomainConflictMessage(dup, excludeID))
		return companyFormResult{}, utils.ErrHandled
	}

	return companyFormResult{Domain: domain, Status: status}, nil
}

type CompanyHandler struct {
	DB *gorm.DB
}

func NewCompanyHandler(db *gorm.DB) *CompanyHandler {
	return &CompanyHandler{DB: db}
}

// List godoc
// @Summary List companies
// @Description Paginated, filterable Company list, each row annotated with last_activity_at (from any company-scoped Activity). Filters: status, tag, industry, search (name), stale_days, has_won_deal.
// @Tags companies
// @Security BearerAuth
// @Produce json
// @Param status query string false "active or archived"
// @Param tag query string false "Filter by Company tag"
// @Param industry query string false "Filter by industry"
// @Param search query string false "Search by name"
// @Param stale_days query int false "Filter to companies with no activity in N days"
// @Param has_won_deal query bool false "Filter to companies with (or without) a won Deal"
// @Param sort query string false "Sort field, prefix - for descending (created_at, name, industry)"
// @Param page query int false "Page number"
// @Param per_page query int false "Items per page"
// @Success 200 {object} map[string]interface{} "Paginated company list (data, page, per_page, total)"
// @Router /companies [get]
func (h *CompanyHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := applyCompanyFilters(h.DB.Model(&models.Company{}), c)
	query = withLastActivityAt(query)

	var total int64
	query.Count(&total)

	var companies []companyWithActivity
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true, "industry": true}, "-created_at")
	if err := query.Select("companies.*, last_company_activity.last_activity_at as last_activity_at").
		Limit(perPage).Offset(offset).Find(&companies).Error; err != nil {
		return utils.Internal(c, "Failed to list companies")
	}
	return utils.List(c, companies, page, perPage, total)
}

type companyForm struct {
	Name        string   `json:"name"`
	Industry    string   `json:"industry"`
	Size        string   `json:"size"`
	RevenueSize string   `json:"revenue_size"`
	Website     string   `json:"website"`
	Tags        []string `json:"tags"`
	Notes       string   `json:"notes"`
	Status      string   `json:"status"`
	LegalName   *string  `json:"legal_name"`
	Address     *string  `json:"address"`
	TaxID       *string  `json:"tax_id"`
}

// Create godoc
// @Summary Create a company
// @Description Creates a Company. name is required; size/revenue_size must each match an active configured option (see /admin/company-sizes, /admin/revenue-sizes). industry is free text — any non-empty value is auto-registered as an active /admin/industries option if it isn't one already. domain is derived server-side from website.
// @Tags companies
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body companyForm true "Company fields"
// @Success 201 {object} models.Company
// @Failure 400 {object} map[string]interface{} "Invalid body or missing name"
// @Router /companies [post]
func (h *CompanyHandler) Create(c *fiber.Ctx) error {
	var form companyForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	result, err := validateCompanyForm(c, h.DB, form, 0)
	if err != nil {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	company := models.Company{
		Name: form.Name, Industry: form.Industry, Size: form.Size, RevenueSize: form.RevenueSize, Website: form.Website,
		Domain: result.Domain,
		Tags:   pq.StringArray(normalizeTags(form.Tags)), Notes: form.Notes,
		Status:    result.Status,
		LegalName: form.LegalName, Address: form.Address, TaxID: form.TaxID,
	}
	if company.Status == "" {
		company.Status = models.StatusActive
	}
	company.CreatedBy = &actorID
	company.UpdatedBy = &actorID
	if err := h.DB.Create(&company).Error; err != nil {
		// validateCompanyForm's own domain-conflict pre-check can lose a race
		// to a concurrent Create for the same domain — the DB's unique index
		// (database.go) is what actually catches that; re-resolve it into the
		// same friendly 409 rather than a generic 500.
		if dup, lookupErr := conflictingCompanyDomain(h.DB, result.Domain, 0); lookupErr == nil && dup != nil {
			return utils.Conflict(c, companyDomainConflictMessage(dup, 0))
		}
		return utils.Internal(c, "Failed to create company")
	}
	return utils.Created(c, company)
}

// Get godoc
// @Summary Get a company
// @Description Returns a single Company, including its last_activity_at.
// @Tags companies
// @Security BearerAuth
// @Produce json
// @Param id path int true "Company ID"
// @Success 200 {object} models.Company
// @Failure 404 {object} map[string]interface{} "Company not found"
// @Router /companies/{id} [get]
func (h *CompanyHandler) Get(c *fiber.Ctx) error {
	var company companyWithActivity
	query := withLastActivityAt(h.DB.Model(&models.Company{})).
		Select("companies.*, last_company_activity.last_activity_at as last_activity_at")
	if err := query.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}
	return utils.OK(c, company)
}

// Update godoc
// @Summary Update a company
// @Description Updates a Company. size/revenue_size must each match an active configured option; industry is free text and auto-registers a new active /admin/industries option if needed; domain is re-derived from website.
// @Tags companies
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Company ID"
// @Param body body companyForm true "Company fields"
// @Success 200 {object} models.Company
// @Failure 400 {object} map[string]interface{} "Invalid body or invalid option"
// @Failure 404 {object} map[string]interface{} "Company not found"
// @Router /companies/{id} [put]
func (h *CompanyHandler) Update(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}

	var form companyForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	result, err := validateCompanyForm(c, h.DB, form, company.ID)
	if err != nil {
		return nil
	}

	company.Name, company.Industry, company.Size, company.RevenueSize, company.Website = form.Name, form.Industry, form.Size, form.RevenueSize, form.Website
	company.Domain = result.Domain
	company.Tags = pq.StringArray(normalizeTags(form.Tags))
	company.Notes = form.Notes
	company.LegalName, company.Address, company.TaxID = form.LegalName, form.Address, form.TaxID
	if result.Status != "" {
		company.Status = result.Status
	}
	actorID := middleware.CurrentUserID(c)
	company.UpdatedBy = &actorID

	if err := h.DB.Save(&company).Error; err != nil {
		// Same race as Create — see validateCompanyForm's doc.
		if dup, lookupErr := conflictingCompanyDomain(h.DB, result.Domain, company.ID); lookupErr == nil && dup != nil {
			return utils.Conflict(c, companyDomainConflictMessage(dup, company.ID))
		}
		return utils.Internal(c, "Failed to update company")
	}
	return utils.OK(c, company)
}

// Delete godoc
// @Summary Delete a company
// @Description Soft-delete (AuditedModel) — recoverable via Restore/Trash below. Never a hard delete, since Deals/Contacts/Payments reference company_id.
// @Tags companies
// @Security BearerAuth
// @Param id path int true "Company ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{} "Company not found"
// @Router /companies/{id} [delete]
func (h *CompanyHandler) Delete(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}
	actorID := middleware.CurrentUserID(c)
	if err := utils.GenericSoftDelete(h.DB, &company, actorID); err != nil {
		return utils.Internal(c, "Failed to delete company")
	}
	return utils.NoContent(c)
}

// Trash godoc
// @Summary List deleted companies (Admin/Sales Manager only)
// @Description Returns soft-deleted Companies.
// @Tags companies
// @Security BearerAuth
// @Produce json
// @Param search query string false "Search by name"
// @Success 200 {object} map[string]interface{} "Paginated company list (data, page, per_page, total)"
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Router /companies/trash [get]
func (h *CompanyHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.Company](c, h.DB, "Failed to list deleted companies", "name")
}

// Restore godoc
// @Summary Restore a deleted company (Admin/Sales Manager only)
// @Description Un-deletes a soft-deleted Company.
// @Tags companies
// @Security BearerAuth
// @Produce json
// @Param id path int true "Company ID"
// @Success 200 {object} models.Company
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Failure 404 {object} map[string]interface{} "Deleted company not found"
// @Router /companies/{id}/restore [post]
func (h *CompanyHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.Company](c, h.DB, "Deleted company not found", "Failed to restore company")
}

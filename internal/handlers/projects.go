package handlers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ProjectHandler struct {
	DB *gorm.DB
}

func NewProjectHandler(db *gorm.DB) *ProjectHandler {
	return &ProjectHandler{DB: db}
}

// ListForCompany godoc
// @Summary List a Company's Projects
// @Description Lists a Company's Projects, newest first — powers the Company profile's "Projects" section. FR-CRM-070.
// @Tags projects
// @Security BearerAuth
// @Produce json
// @Param companyId path int true "Company ID"
// @Success 200 {array} models.Project
// @Router /companies/{companyId}/projects [get]
func (h *ProjectHandler) ListForCompany(c *fiber.Ctx) error {
	var projects []models.Project
	if err := h.DB.Where("company_id = ?", c.Params("companyId")).Order("created_at DESC").Find(&projects).Error; err != nil {
		return utils.Internal(c, "Failed to list projects")
	}
	return utils.OK(c, projects)
}

// Get godoc
// @Summary Get a project by ID
// @Description Returns a single Project by ID. Used by the Open API (GET /open/projects/:id) as well as any future top-level staff route.
// @Tags projects
// @Security BearerAuth
// @Produce json
// @Param id path int true "Project ID"
// @Success 200 {object} models.Project
// @Failure 404 {object} map[string]interface{} "Project not found"
// @Router /projects/{id} [get]
func (h *ProjectHandler) Get(c *fiber.Ctx) error {
	var project models.Project
	if err := utils.FindByID(c, h.DB, &project, "Project not found"); err != nil {
		return nil
	}
	return utils.OK(c, project)
}

type projectWithCompany struct {
	models.Project
	CompanyName string `json:"company_name"`
}

// List godoc
// @Summary List projects
// @Description Paginated cross-company Project view (the single-Company view ListForCompany can't provide) — merges the Company name into each Project. Open to any authenticated role; no field-level RBAC applies to List (the Production-role field restriction only affects Update). FR-CRM-067.
// @Tags projects
// @Security BearerAuth
// @Produce json
// @Param status query string false "Filter by status"
// @Param company_id query int false "Filter by Company ID"
// @Param sort query string false "Sort field, optionally prefixed with - for descending (created_at, name, target_end_date)"
// @Param page query int false "Page number"
// @Param per_page query int false "Items per page"
// @Success 200 {object} map[string]interface{} "Paginated project list (data, page, per_page, total)"
// @Router /projects [get]
func (h *ProjectHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := applyProjectFilters(h.DB.Model(&models.Project{}), c)

	var total int64
	query.Count(&total)

	var projects []models.Project
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true, "target_end_date": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&projects).Error; err != nil {
		return utils.Internal(c, "Failed to list projects")
	}

	companyIDs := make([]uint, 0, len(projects))
	for _, p := range projects {
		companyIDs = append(companyIDs, p.CompanyID)
	}
	var companies []models.Company
	if len(companyIDs) > 0 {
		h.DB.Where("id IN ?", companyIDs).Find(&companies)
	}
	companyNameByID := make(map[uint]string, len(companies))
	for _, co := range companies {
		companyNameByID[co.ID] = co.Name
	}

	result := make([]projectWithCompany, 0, len(projects))
	for _, p := range projects {
		result = append(result, projectWithCompany{Project: p, CompanyName: companyNameByID[p.CompanyID]})
	}
	return utils.List(c, result, page, perPage, total)
}

type projectForm struct {
	DealID               *uint                `json:"deal_id"`
	Name                 string               `json:"name"`
	Status               models.ProjectStatus `json:"status"`
	StartDate            *time.Time           `json:"start_date"`
	TargetEndDate        *time.Time           `json:"target_end_date"`
	ExpectedProposalDate *time.Time           `json:"expected_proposal_date"`
	ExpectedStartDate    *time.Time           `json:"expected_start_date"`
	ProductionReference  *string              `json:"production_reference"`
	Notes                string               `json:"notes"`
}

// Create godoc
// @Summary Create a project (Admin/SalesRep/SalesManager only)
// @Description Admin/SalesRep/SalesManager only. Creates a Project for a Company, manually or when a Deal is marked Won. FR-CRM-068. start_date defaults to now and status defaults to "Not Started" when omitted.
// @Tags projects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param companyId path int true "Company ID"
// @Param body body projectForm true "Project fields"
// @Success 201 {object} models.Project
// @Failure 400 {object} map[string]interface{} "Invalid body or missing name"
// @Failure 404 {object} map[string]interface{} "Company not found"
// @Router /companies/{companyId}/projects [post]
func (h *ProjectHandler) Create(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("companyId")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}

	var form projectForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	return h.saveNewProject(c, newProjectFromForm(form, company.ID))
}

// newProjectFromForm builds a models.Project for companyID from a
// projectForm, applying Create's own defaults (start_date -> now, status ->
// "Not Started") — shared by Create (companyId from the path) and CreateOpen
// (company_id from the body) so the two don't drift.
func newProjectFromForm(form projectForm, companyID uint) models.Project {
	project := models.Project{
		CompanyID: companyID, DealID: form.DealID, Name: form.Name, Status: form.Status,
		TargetEndDate: form.TargetEndDate, ProductionReference: form.ProductionReference, Notes: form.Notes,
		ExpectedProposalDate: form.ExpectedProposalDate, ExpectedStartDate: form.ExpectedStartDate,
	}
	if form.StartDate != nil {
		project.StartDate = *form.StartDate
	} else {
		project.StartDate = time.Now()
	}
	if project.Status == "" {
		project.Status = models.ProjectStatusNotStarted
	}
	return project
}

// saveNewProject stamps created_by/updated_by and persists project, writing
// the standard Created/Internal response — shared by Create and CreateOpen.
func (h *ProjectHandler) saveNewProject(c *fiber.Ctx, project models.Project) error {
	actorID := middleware.CurrentUserID(c)
	project.CreatedBy = &actorID
	project.UpdatedBy = &actorID
	if err := h.DB.Create(&project).Error; err != nil {
		return utils.Internal(c, "Failed to create project")
	}
	return utils.Created(c, project)
}

// openProjectForm is projectForm plus company_id — the Open API has no
// company-scoped path segment (unlike the staff-facing nested
// POST /companies/:companyId/projects) so the target Company is named in the
// body instead.
type openProjectForm struct {
	CompanyID uint `json:"company_id"`
	projectForm
}

// CreateOpen godoc
// @Summary Create a project (Open API)
// @Description Creates a Project for a Company via the Open API. Unlike the staff-facing POST /companies/{companyId}/projects, company_id is supplied in the request body rather than the path. start_date defaults to now and status defaults to "Not Started" when omitted.
// @Tags projects
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param body body openProjectForm true "Project fields, including company_id"
// @Success 201 {object} models.Project
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 404 {object} map[string]interface{} "Company not found"
// @Failure 422 {object} map[string]interface{} "Missing name or company_id"
// @Router /open/projects [post]
func (h *ProjectHandler) CreateOpen(c *fiber.Ctx) error {
	var form openProjectForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.CompanyID == 0 {
		return utils.ValidationError(c, "company_id is required", map[string][]string{"company_id": {"required"}})
	}
	var company models.Company
	if err := h.DB.First(&company, form.CompanyID).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	return h.saveNewProject(c, newProjectFromForm(form.projectForm, company.ID))
}

// productionFieldForm is the field set Production may touch — §8.3/§1.7.
type productionFieldForm struct {
	Status              *models.ProjectStatus `json:"status"`
	ProductionReference *string               `json:"production_reference"`
}

// productionAllowedKeys are the only JSON body keys Production may send.
var productionAllowedKeys = map[string]bool{"status": true, "production_reference": true}

// Update godoc
// @Summary Update a project
// @Description Any authenticated role may call this endpoint, but field access is enforced inside the handler by the caller's role (§8.3/§1.7): Sales/Admin/other roles may update any field in projectForm; a caller with the Production role is restricted to only status and production_reference — sending any other JSON body key as Production returns 403. Writes an audit-log entry when status changes (FR-CRM-082).
// @Tags projects
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Project ID"
// @Param body body projectForm true "Project fields (Production role: status/production_reference only)"
// @Success 200 {object} models.Project
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 403 {object} map[string]interface{} "Production role attempted to update a field other than status/production_reference"
// @Failure 404 {object} map[string]interface{} "Project not found"
// @Router /projects/{id} [patch]
func (h *ProjectHandler) Update(c *fiber.Ctx) error {
	var project models.Project
	if err := utils.FindByID(c, h.DB, &project, "Project not found"); err != nil {
		return nil
	}
	oldStatus := project.Status

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &raw); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}

	if middleware.CurrentRole(c) == models.RoleProduction {
		for key := range raw {
			if !productionAllowedKeys[key] {
				return utils.Forbidden(c, "Production may only update status and production_reference")
			}
		}

		var form productionFieldForm
		if err := c.BodyParser(&form); err != nil {
			return utils.BadRequest(c, "Invalid request body")
		}
		if form.Status != nil {
			project.Status = *form.Status
		}
		if form.ProductionReference != nil {
			project.ProductionReference = form.ProductionReference
		}
	} else {
		var form projectForm
		if err := c.BodyParser(&form); err != nil {
			return utils.BadRequest(c, "Invalid request body")
		}
		if form.Name != "" {
			project.Name = form.Name
		}
		if _, ok := raw["deal_id"]; ok {
			project.DealID = form.DealID
		}
		if form.Status != "" {
			project.Status = form.Status
		}
		if form.StartDate != nil {
			project.StartDate = *form.StartDate
		}
		if _, ok := raw["target_end_date"]; ok {
			project.TargetEndDate = form.TargetEndDate
		}
		if _, ok := raw["expected_proposal_date"]; ok {
			project.ExpectedProposalDate = form.ExpectedProposalDate
		}
		if _, ok := raw["expected_start_date"]; ok {
			project.ExpectedStartDate = form.ExpectedStartDate
		}
		if _, ok := raw["production_reference"]; ok {
			project.ProductionReference = form.ProductionReference
		}
		if _, ok := raw["notes"]; ok {
			project.Notes = form.Notes
		}
	}

	actorID := middleware.CurrentUserID(c)
	project.UpdatedBy = &actorID

	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Save(&project).Error },
		oldStatus != project.Status, "project", project.ID, "status_changed",
		models.JSONMap{"status": oldStatus}, models.JSONMap{"status": project.Status}, actorID)
	if err != nil {
		return utils.Internal(c, "Failed to update project")
	}
	return utils.OK(c, project)
}

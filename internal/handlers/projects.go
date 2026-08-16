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

// ListForCompany — GET /companies/:companyId/projects (any authenticated).
func (h *ProjectHandler) ListForCompany(c *fiber.Ctx) error {
	var projects []models.Project
	if err := h.DB.Where("company_id = ?", c.Params("companyId")).Order("created_at DESC").Find(&projects).Error; err != nil {
		return utils.Internal(c, "Failed to list projects")
	}
	return utils.OK(c, projects)
}

type projectWithCompany struct {
	models.Project
	CompanyName string `json:"company_name"`
}

// List — GET /projects (any authenticated). The cross-company view
// ListForCompany can't provide — merges the Company name in the same way
// ProductHandler.ListForCompany merges Product into CustomerProduct.
func (h *ProjectHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Project{})

	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}

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
	DealID              *uint                `json:"deal_id"`
	Name                string               `json:"name"`
	Status              models.ProjectStatus `json:"status"`
	StartDate           *time.Time           `json:"start_date"`
	TargetEndDate       *time.Time           `json:"target_end_date"`
	ProductionReference *string              `json:"production_reference"`
	Notes               string               `json:"notes"`
}

// Create — POST /companies/:companyId/projects (Sales/Admin, route-gated).
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

	actorID := middleware.CurrentUserID(c)
	project := models.Project{
		CompanyID: company.ID, DealID: form.DealID, Name: form.Name, Status: form.Status,
		TargetEndDate: form.TargetEndDate, ProductionReference: form.ProductionReference, Notes: form.Notes,
	}
	if form.StartDate != nil {
		project.StartDate = *form.StartDate
	} else {
		project.StartDate = time.Now()
	}
	if project.Status == "" {
		project.Status = models.ProjectStatusNotStarted
	}
	project.CreatedBy = &actorID
	project.UpdatedBy = &actorID
	if err := h.DB.Create(&project).Error; err != nil {
		return utils.Internal(c, "Failed to create project")
	}
	return utils.Created(c, project)
}

// productionFieldForm is the field set Production may touch — §8.3/§1.7.
type productionFieldForm struct {
	Status              *models.ProjectStatus `json:"status"`
	ProductionReference *string               `json:"production_reference"`
}

// productionAllowedKeys are the only JSON body keys Production may send.
var productionAllowedKeys = map[string]bool{"status": true, "production_reference": true}

// Update — PATCH /projects/:id. Sales/Admin can update any field. Production
// is scoped to status and production_reference only — enforced field-level by
// inspecting the raw JSON body's keys, not just endpoint-level.
func (h *ProjectHandler) Update(c *fiber.Ctx) error {
	var project models.Project
	if err := h.DB.First(&project, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Project not found")
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

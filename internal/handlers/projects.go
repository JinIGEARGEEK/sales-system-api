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
		project.DealID = form.DealID
		if form.Status != "" {
			project.Status = form.Status
		}
		if form.StartDate != nil {
			project.StartDate = *form.StartDate
		}
		project.TargetEndDate = form.TargetEndDate
		project.ProductionReference = form.ProductionReference
		project.Notes = form.Notes
	}

	if err := h.DB.Save(&project).Error; err != nil {
		return utils.Internal(c, "Failed to update project")
	}
	return utils.OK(c, project)
}

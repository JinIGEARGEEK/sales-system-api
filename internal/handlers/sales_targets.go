package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// SalesTargetHandler — Admin CRUD for per-quarter/per-year sales targets
// (FR-CRM-092). A row here overrides AppSettings.QuarterlySalesTarget/4 for
// its specific (year, quarter) in dashboard.go's pipeline_coverage_ratio
// calc; deleting a row simply reverts that period back to the flat fallback.
type SalesTargetHandler struct {
	DB *gorm.DB
}

func NewSalesTargetHandler(db *gorm.DB) *SalesTargetHandler {
	return &SalesTargetHandler{DB: db}
}

// List godoc
// @Summary List sales targets
// @Description Admin-only. Returns every configured per-quarter/per-year sales target, optionally filtered to one year, ordered oldest-to-newest.
// @Tags admin/sales-targets
// @Security BearerAuth
// @Produce json
// @Param year query int false "Filter to a single year"
// @Success 200 {array} models.SalesTarget
// @Router /admin/sales-targets [get]
func (h *SalesTargetHandler) List(c *fiber.Ctx) error {
	query := h.DB.Model(&models.SalesTarget{})
	if year := c.Query("year"); year != "" {
		if y, err := strconv.Atoi(year); err == nil {
			query = query.Where("year = ?", y)
		}
	}

	var targets []models.SalesTarget
	if err := query.Order("year ASC, quarter ASC").Find(&targets).Error; err != nil {
		return utils.Internal(c, "Failed to list sales targets")
	}
	return utils.OK(c, targets)
}

type salesTargetForm struct {
	Year        int    `json:"year"`
	Quarter     int    `json:"quarter"`
	TargetValue *int64 `json:"target_value"`
}

// validate checks the shared shape of a create/update body, writing the 422
// response itself and returning false on failure — same convention as
// settings.go's requireNonNegative.
func (f salesTargetForm) validate(c *fiber.Ctx) bool {
	fields := map[string][]string{}
	if f.Quarter < 1 || f.Quarter > 4 {
		fields["quarter"] = []string{"must be between 1 and 4"}
	}
	if f.Year < 2000 || f.Year > 2100 {
		fields["year"] = []string{"must be a valid year"}
	}
	if f.TargetValue == nil {
		fields["target_value"] = []string{"required"}
	} else if *f.TargetValue < 0 {
		fields["target_value"] = []string{"must be >= 0"}
	}
	if len(fields) > 0 {
		_ = utils.ValidationError(c, "Invalid sales target", fields)
		return false
	}
	return true
}

// Create godoc
// @Summary Create a sales target
// @Description Admin-only. One row per (year, quarter); creating a second row for the same period is rejected — PATCH the existing one instead. quarter must be 1-4, year must be a valid year, and target_value must be non-negative.
// @Tags admin/sales-targets
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body salesTargetForm true "Sales target fields"
// @Success 201 {object} models.SalesTarget
// @Failure 400 {object} map[string]interface{}
// @Router /admin/sales-targets [post]
func (h *SalesTargetHandler) Create(c *fiber.Ctx) error {
	var form salesTargetForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !form.validate(c) {
		return nil
	}

	var existing models.SalesTarget
	if err := h.DB.Where("year = ? AND quarter = ?", form.Year, form.Quarter).First(&existing).Error; err == nil {
		return utils.ValidationError(c, "A target already exists for this year/quarter", map[string][]string{
			"quarter": {"A target for this year and quarter already exists — edit it instead"},
		})
	}

	actorID := middleware.CurrentUserID(c)
	target := models.SalesTarget{
		Year: form.Year, Quarter: form.Quarter, TargetValue: *form.TargetValue,
		CreatedBy: &actorID, UpdatedBy: &actorID,
	}
	after := models.JSONMap{"year": target.Year, "quarter": target.Quarter, "target_value": target.TargetValue}

	// Not SaveWithAudit here deliberately: that helper takes entityID as a
	// plain uint argument, evaluated at the call site before its save closure
	// runs — fine for every other caller in this codebase (Update/Reassign on
	// a row that already has an ID), but target.ID is still its zero value at
	// that point for a brand-new row, so entity_id would be wrong (always 0)
	// in the written audit-log entry. Run the transaction directly instead so
	// WriteAuditLog reads target.ID after Create has actually populated it.
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&target).Error; err != nil {
			return err
		}
		return utils.WriteAuditLog(tx, "sales_target", target.ID, "created", nil, after, actorID)
	})
	if err != nil {
		return utils.Internal(c, "Failed to create sales target")
	}
	InvalidateDashboardCache()
	return utils.Created(c, target)
}

// Update godoc
// @Summary Update a sales target
// @Description Admin-only. Only target_value is editable in practice (year/quarter identify the row); changing year/quarter to collide with another existing row is rejected the same way Create is.
// @Tags admin/sales-targets
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Sales target ID"
// @Param body body salesTargetForm true "Sales target fields"
// @Success 200 {object} models.SalesTarget
// @Failure 404 {object} map[string]interface{}
// @Router /admin/sales-targets/{id} [patch]
func (h *SalesTargetHandler) Update(c *fiber.Ctx) error {
	var target models.SalesTarget
	if err := h.DB.First(&target, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Sales target not found")
	}

	var form salesTargetForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !form.validate(c) {
		return nil
	}

	if form.Year != target.Year || form.Quarter != target.Quarter {
		var collision models.SalesTarget
		if err := h.DB.Where("year = ? AND quarter = ? AND id != ?", form.Year, form.Quarter, target.ID).
			First(&collision).Error; err == nil {
			return utils.ValidationError(c, "A target already exists for this year/quarter", map[string][]string{
				"quarter": {"A target for this year and quarter already exists"},
			})
		}
	}

	before := models.JSONMap{"year": target.Year, "quarter": target.Quarter, "target_value": target.TargetValue}
	changed := target.TargetValue != *form.TargetValue || target.Year != form.Year || target.Quarter != form.Quarter

	target.Year, target.Quarter, target.TargetValue = form.Year, form.Quarter, *form.TargetValue
	actorID := middleware.CurrentUserID(c)
	target.UpdatedBy = &actorID
	after := models.JSONMap{"year": target.Year, "quarter": target.Quarter, "target_value": target.TargetValue}

	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Save(&target).Error },
		changed, "sales_target", target.ID, "updated", before, after, actorID)
	if err != nil {
		return utils.Internal(c, "Failed to update sales target")
	}
	if changed {
		InvalidateDashboardCache()
	}
	return utils.OK(c, target)
}

// Delete godoc
// @Summary Delete a sales target
// @Description Admin-only. A hard delete (unlike PipelineStage's soft-delete) — nothing else references a SalesTarget row by ID, and removing one just reverts that period to the flat AppSettings.QuarterlySalesTarget/4 fallback.
// @Tags admin/sales-targets
// @Security BearerAuth
// @Param id path int true "Sales target ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{}
// @Router /admin/sales-targets/{id} [delete]
func (h *SalesTargetHandler) Delete(c *fiber.Ctx) error {
	var target models.SalesTarget
	if err := h.DB.First(&target, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Sales target not found")
	}

	before := models.JSONMap{"year": target.Year, "quarter": target.Quarter, "target_value": target.TargetValue}
	actorID := middleware.CurrentUserID(c)

	err := utils.SaveWithAudit(h.DB, func(tx *gorm.DB) error { return tx.Delete(&target).Error },
		true, "sales_target", target.ID, "deleted", before, nil, actorID)
	if err != nil {
		return utils.Internal(c, "Failed to delete sales target")
	}
	InvalidateDashboardCache()
	return utils.NoContent(c)
}

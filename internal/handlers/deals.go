package handlers

import (
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
	query := h.DB.Model(&models.Deal{})

	if v := c.Query("stage"); v != "" {
		query = query.Where("stage = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("business_unit"); v != "" {
		query = query.Where("business_unit = ?", v)
	}
	if v := c.Query("channel"); v != "" {
		query = query.Where("channel = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("title ILIKE ?", "%"+v+"%")
	}

	var total int64
	query.Count(&total)

	var deals []models.Deal
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "title": true, "value": true}, "-created_at")
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
}

// Create — POST /deals.
func (h *DealHandler) Create(c *fiber.Ctx) error {
	var form dealForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Title == "" || form.CompanyID == 0 || form.ContactID == 0 {
		return utils.ValidationError(c, "company_id, contact_id and title are required", map[string][]string{
			"company_id": {"required"},
			"contact_id": {"required"},
			"title":      {"required"},
		})
	}
	if !CanWrite(c, form.AssignedTo) {
		return utils.Forbidden(c, "Cannot assign a deal to another sales rep")
	}

	deal := models.Deal{
		CompanyID: form.CompanyID, ContactID: form.ContactID, Title: form.Title, Value: form.Value,
		Stage: form.Stage, Status: form.Status, ExpectedCloseDate: form.ExpectedCloseDate,
		AssignedTo: form.AssignedTo, Channel: form.Channel,
		BusinessUnit: form.BusinessUnit, BusinessUnitItem: form.BusinessUnitItem,
	}
	if deal.Stage == "" {
		deal.Stage = models.DealStageLead
	}
	if deal.Status == "" {
		deal.Status = models.DealStatusOpen
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
	if form.Title == "" || form.CompanyID == 0 || form.ContactID == 0 {
		return utils.ValidationError(c, "company_id, contact_id and title are required", map[string][]string{
			"company_id": {"required"},
			"contact_id": {"required"},
			"title":      {"required"},
		})
	}

	deal.CompanyID, deal.ContactID, deal.Title, deal.Value = form.CompanyID, form.ContactID, form.Title, form.Value
	deal.Stage, deal.Status, deal.ExpectedCloseDate = form.Stage, form.Status, form.ExpectedCloseDate
	deal.AssignedTo, deal.Channel = form.AssignedTo, form.Channel
	deal.BusinessUnit, deal.BusinessUnitItem = form.BusinessUnit, form.BusinessUnitItem

	if err := h.DB.Save(&deal).Error; err != nil {
		return utils.Internal(c, "Failed to update deal")
	}
	return utils.OK(c, deal)
}

// Delete — DELETE /deals/:id (hard delete, HardDeleteModel).
func (h *DealHandler) Delete(c *fiber.Ctx) error {
	var deal models.Deal
	if err := h.DB.First(&deal, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Deal not found")
	}
	if !CanWrite(c, deal.AssignedTo) {
		return utils.Forbidden(c, "Not authorized to delete this deal")
	}
	if err := h.DB.Delete(&deal).Error; err != nil {
		return utils.Internal(c, "Failed to delete deal")
	}
	return utils.NoContent(c)
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

	before := models.JSONMap{"stage": deal.Stage, "status": deal.Status}
	oldStage := deal.Stage
	deal.Stage = form.Stage
	switch deal.Stage {
	case models.DealStageWon:
		deal.Status = models.DealStatusWon
		// Hook point: FR-CRM-064 auto-creates/updates a CustomerProduct(status: Active)
		// per Product on this Deal's accepted Quote — deferred until Quotes have a
		// real "accepted" flow to hang the side effect off.
	case models.DealStageLost:
		deal.Status = models.DealStatusLost
	default:
		if deal.Status != models.DealStatusWon && deal.Status != models.DealStatusLost {
			deal.Status = models.DealStatusOpen
		}
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

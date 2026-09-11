package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// APIKeyHandler manages models.APIKey — the credentials external integrations
// use against the /open/* group (see middleware.RequireAPIKey). Admin-only,
// same access level as every other /admin/* config resource.
type APIKeyHandler struct {
	DB *gorm.DB
}

func NewAPIKeyHandler(db *gorm.DB) *APIKeyHandler {
	return &APIKeyHandler{DB: db}
}

// List godoc
// @Summary List API keys (Admin only)
// @Description Returns every API key's metadata — never the raw secret, which is only ever shown once at Create.
// @Tags admin
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "Paginated API key list (data, page, per_page, total)"
// @Router /admin/api-keys [get]
func (h *APIKeyHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.APIKey{})

	var total int64
	query.Count(&total)

	var keys []models.APIKey
	if err := query.Order("created_at DESC").Limit(perPage).Offset(offset).Find(&keys).Error; err != nil {
		return utils.Internal(c, "Failed to list API keys")
	}
	return utils.List(c, keys, page, perPage, total)
}

type apiKeyForm struct {
	Name        string `json:"name"`
	OwnerUserID uint   `json:"owner_user_id"`
}

// Create godoc
// @Summary Create an API key (Admin only)
// @Description Creates a new open-API key acting as owner_user_id (must be an active existing User — the key inherits that user's role and attributes created_by/updated_by to them on every /open/* write). Returns the raw key in the "key" field — shown exactly once; only its hash is ever persisted.
// @Tags admin
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body apiKeyForm true "API key fields"
// @Success 201 {object} map[string]interface{} "Created key metadata plus the one-time raw key"
// @Failure 400 {object} map[string]interface{} "Invalid body, missing fields, or unknown/inactive owner_user_id"
// @Router /admin/api-keys [post]
func (h *APIKeyHandler) Create(c *fiber.Ctx) error {
	var form apiKeyForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" || form.OwnerUserID == 0 {
		return utils.ValidationError(c, "name and owner_user_id are required", map[string][]string{
			"name":          {"required"},
			"owner_user_id": {"required"},
		})
	}

	var owner models.User
	if err := h.DB.First(&owner, form.OwnerUserID).Error; err != nil {
		return utils.ValidationError(c, "owner_user_id does not match an existing User", map[string][]string{"owner_user_id": {"invalid"}})
	}
	if !owner.IsActive {
		return utils.ValidationError(c, "owner_user_id must be an active User", map[string][]string{"owner_user_id": {"invalid"}})
	}

	raw, hash, err := utils.GenerateAPIKey()
	if err != nil {
		return utils.Internal(c, "Failed to generate API key")
	}

	actorID := middleware.CurrentUserID(c)
	key := models.APIKey{
		Name:        form.Name,
		KeyHash:     hash,
		KeyPrefix:   raw[:utils.APIKeyPrefixLen],
		OwnerUserID: form.OwnerUserID,
		IsActive:    true,
		CreatedBy:   &actorID,
	}
	if err := h.DB.Create(&key).Error; err != nil {
		return utils.Internal(c, "Failed to create API key")
	}

	return utils.Created(c, fiber.Map{
		"api_key": key,
		// key is the only time the raw secret is ever returned — not
		// recoverable afterward, same as a generated password.
		"key": raw,
	})
}

// Revoke godoc
// @Summary Revoke an API key (Admin only)
// @Description Deactivates a key immediately (RequireAPIKey rejects it within apiKeyCacheTTL at the latest) without deleting its row, so its audit trail (who created it, who revoked it, when) is preserved.
// @Tags admin
// @Security BearerAuth
// @Param id path int true "API key ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{} "API key not found"
// @Router /admin/api-keys/{id}/revoke [post]
func (h *APIKeyHandler) Revoke(c *fiber.Ctx) error {
	var key models.APIKey
	if err := h.DB.First(&key, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "API key not found")
	}

	actorID := middleware.CurrentUserID(c)
	now := time.Now()
	key.IsActive = false
	key.RevokedAt = &now
	key.RevokedBy = &actorID
	if err := h.DB.Save(&key).Error; err != nil {
		return utils.Internal(c, "Failed to revoke API key")
	}
	middleware.InvalidateAPIKeyCache(key.KeyHash)
	return utils.NoContent(c)
}

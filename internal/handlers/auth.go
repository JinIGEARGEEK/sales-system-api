package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type AuthHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

func NewAuthHandler(db *gorm.DB, cfg *config.Config) *AuthHandler {
	return &AuthHandler{DB: db, Cfg: cfg}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login — POST /auth/login, api-system-spec.md §1.2.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if req.Username == "" || req.Password == "" {
		return utils.ValidationError(c, "username and password are required", map[string][]string{
			"username": {"required"},
			"password": {"required"},
		})
	}

	var user models.User
	if err := h.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		return utils.Unauthorized(c, "Invalid username or password")
	}
	if !user.IsActive {
		return utils.Unauthorized(c, "Account is inactive")
	}
	if !utils.CheckPassword(user.PasswordHash, req.Password) {
		return utils.Unauthorized(c, "Invalid username or password")
	}

	token, err := utils.GenerateToken(h.Cfg.JWTSecret, h.Cfg.JWTExpiryHr, user.ID, user.Role)
	if err != nil {
		return utils.Internal(c, "Failed to generate token")
	}

	now := time.Now()
	h.DB.Model(&user).Update("latest_login", &now)
	user.LatestLogin = &now

	return utils.OK(c, fiber.Map{
		"access_token": token,
		"user":         user,
	})
}

// Logout — POST /auth/logout. No server-side blocklist for v1 (stateless JWT);
// the frontend clears localStorage regardless per §1.2.
func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// Me — GET /auth/me.
func (h *AuthHandler) Me(c *fiber.Ctx) error {
	var user models.User
	if err := h.DB.First(&user, middleware.CurrentUserID(c)).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}
	return utils.OK(c, user)
}

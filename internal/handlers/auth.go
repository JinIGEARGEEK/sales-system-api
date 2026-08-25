package handlers

import (
	"fmt"
	"log"
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
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login — POST /auth/login, api-system-spec.md §1.2.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := utils.RequireFields(c,
		utils.Field{Name: "email", Value: req.Email},
		utils.Field{Name: "password", Value: req.Password},
	); err != nil {
		return err
	}

	var user models.User
	if err := h.DB.Where("email = ?", req.Email).First(&user).Error; err != nil {
		return utils.Unauthorized(c, "Invalid email or password")
	}
	if !user.IsActive {
		return utils.Unauthorized(c, "Account is inactive")
	}
	if !utils.CheckPassword(user.PasswordHash, req.Password) {
		return utils.Unauthorized(c, "Invalid email or password")
	}

	token, err := utils.GenerateToken(h.Cfg.JWTSecret, h.Cfg.JWTExpiryHr, user.ID, user.Role, user.TokenVersion)
	if err != nil {
		return utils.Internal(c, "Failed to generate token")
	}

	now := time.Now()
	if err := h.DB.Model(&user).Update("latest_login", &now).Error; err != nil {
		// Best-effort: don't fail the login over a bookkeeping write.
		log.Printf("login: failed to record latest_login for user %d: %v", user.ID, err)
	} else {
		user.LatestLogin = &now
	}

	return utils.OK(c, fiber.Map{
		"access_token": token,
		"user":         user,
	})
}

// Logout — POST /auth/logout. Bumps the caller's token_version so the token
// just used (and any other still-valid token issued to this user) fails
// RequireAuth's check from now on — the closest a stateless JWT gets to a
// real server-side revocation without a full token blocklist. The frontend
// also clears localStorage regardless per §1.2; this covers the case where a
// still-live token leaked or is reused after "logout" (shared machine, a
// captured token, an Admin needing an account's sessions killed — see Update).
func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	userID := middleware.CurrentUserID(c)
	if err := h.DB.Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
		return utils.Internal(c, "Failed to log out")
	}
	middleware.InvalidateAuthCache(userID)
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

const minPasswordLength = 8

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

// ChangePassword — POST /auth/change-password. Any authenticated user calls this
// to set their own password, clearing MustChangePassword — the one route
// middleware.RequirePasswordChanged always lets through so a forced-change
// account isn't locked out of the only way to satisfy the requirement.
func (h *AuthHandler) ChangePassword(c *fiber.Ctx) error {
	var req changePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := utils.RequireFields(c,
		utils.Field{Name: "current_password", Value: req.CurrentPassword},
		utils.Field{Name: "new_password", Value: req.NewPassword},
		utils.Field{Name: "confirm_password", Value: req.ConfirmPassword},
	); err != nil {
		return err
	}
	if req.NewPassword != req.ConfirmPassword {
		return utils.ValidationError(c, "new_password and confirm_password must match", map[string][]string{
			"confirm_password": {"must match new_password"},
		})
	}
	if len(req.NewPassword) < minPasswordLength {
		msg := fmt.Sprintf("new_password must be at least %d characters", minPasswordLength)
		return utils.ValidationError(c, msg, map[string][]string{"new_password": {msg}})
	}

	var user models.User
	if err := h.DB.First(&user, middleware.CurrentUserID(c)).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}
	if !utils.CheckPassword(user.PasswordHash, req.CurrentPassword) {
		return utils.Unauthorized(c, "Current password is incorrect")
	}
	if req.NewPassword == req.CurrentPassword {
		return utils.ValidationError(c, "new_password must be different from current password", map[string][]string{
			"new_password": {"must be different from current password"},
		})
	}

	hash, err := utils.HashPassword(req.NewPassword)
	if err != nil {
		return utils.Internal(c, "Failed to hash password")
	}
	user.PasswordHash = hash
	user.MustChangePassword = false
	if err := h.DB.Save(&user).Error; err != nil {
		return utils.Internal(c, "Failed to update password")
	}
	middleware.InvalidateMustChangePassword(user.ID)
	return utils.OK(c, user)
}

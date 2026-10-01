package handlers

import (
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	// Password before IsActive: checking IsActive first told anyone who
	// knew just an email whether that account exists and is deactivated.
	if !utils.CheckPassword(user.PasswordHash, req.Password) {
		return utils.Unauthorized(c, "Invalid email or password")
	}
	if !user.IsActive {
		return utils.Unauthorized(c, "Account is inactive")
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

// bumpTokenVersion revokes every token already issued to user (see
// models.User.TokenVersion) with one atomic increment, reading the new
// version back into user.TokenVersion. tx is the write that made those
// tokens stale; callers InvalidateAuthCache once it commits.
func bumpTokenVersion(tx *gorm.DB, user *models.User) error {
	return tx.Model(user).Where("id = ?", user.ID).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "token_version"}}}).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
}

// Logout — POST /auth/logout. Bumps the caller's token_version so every
// still-valid token issued to them (a leaked one, another device) fails
// RequireAuth from now on — revocation without a token blocklist. The
// frontend also clears its stored token per §1.2.
func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var user models.User
	user.ID = middleware.CurrentUserID(c)
	if err := bumpTokenVersion(h.DB, &user); err != nil {
		return utils.Internal(c, "Failed to log out")
	}
	middleware.InvalidateAuthCache(user.ID)
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

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

// ChangePassword — POST /auth/change-password. Any authenticated user calls this
// to set their own password, clearing MustChangePassword — the one route
// middleware.RequirePasswordChanged always lets through so a forced-change
// account isn't locked out of the only way to satisfy the requirement.
// Revokes every token issued before the change, including the caller's own,
// and returns a replacement as data.access_token next to the user fields.
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
	if len(req.NewPassword) < utils.MinPasswordLength {
		msg := fmt.Sprintf("new_password must be at least %d characters", utils.MinPasswordLength)
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
	// Bumping token_version signs out every other session still holding a
	// token issued under the old password (a leaked token, another device);
	// the caller keeps working via the fresh token in the response.
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return bumpTokenVersion(tx, &user)
	}); err != nil {
		return utils.Internal(c, "Failed to update password")
	}
	middleware.InvalidateMustChangePassword(user.ID)
	middleware.InvalidateAuthCache(user.ID)

	token, err := utils.GenerateToken(h.Cfg.JWTSecret, h.Cfg.JWTExpiryHr, user.ID, user.Role, user.TokenVersion)
	if err != nil {
		return utils.Internal(c, "Failed to generate token")
	}
	return utils.OK(c, changePasswordResponse{User: user, AccessToken: token})
}

// changePasswordResponse keeps the user fields at the top level of `data`
// (what the frontend already reads as the updated User) and adds the
// replacement token alongside them — the caller's current token stops
// working once the password changes.
type changePasswordResponse struct {
	models.User
	AccessToken string `json:"access_token"`
}

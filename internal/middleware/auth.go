package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const (
	LocalUserID = "user_id"
	LocalRole   = "role"
)

// RequireAuth enforces the Bearer JWT convention in api-system-spec.md §1.2 —
// exactly a 401 on missing/expired/invalid token, matching the frontend's
// axios interceptor redirect-to-/login behavior.
func RequireAuth(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			return utils.Unauthorized(c, "Missing or invalid Authorization header")
		}
		tokenString := strings.TrimPrefix(header, "Bearer ")

		claims, err := utils.ParseToken(cfg.JWTSecret, tokenString)
		if err != nil || claims == nil {
			return utils.Unauthorized(c, "Invalid or expired token")
		}

		c.Locals(LocalUserID, claims.UserID)
		c.Locals(LocalRole, claims.Role)
		return c.Next()
	}
}

// RequireRoles enforces §1.7 role checks server-side. Returns 403 (not 401) per
// the spec's table: authenticated but not authorized for this resource/action.
func RequireRoles(roles ...models.Role) fiber.Handler {
	allowed := make(map[models.Role]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals(LocalRole).(models.Role)
		if !allowed[role] {
			return utils.Forbidden(c, "You are not authorized to perform this action")
		}
		return c.Next()
	}
}

func CurrentUserID(c *fiber.Ctx) uint {
	id, _ := c.Locals(LocalUserID).(uint)
	return id
}

func CurrentRole(c *fiber.Ctx) models.Role {
	role, _ := c.Locals(LocalRole).(models.Role)
	return role
}

// IsManager reports whether the current caller can read/manage every rep's
// records — Admin and Sales Manager per §1.7.
func IsManager(c *fiber.Ctx) bool {
	role := CurrentRole(c)
	return role == models.RoleAdmin || role == models.RoleSalesManager
}

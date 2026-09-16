package utils

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// FindByID loads dest via db.First(dest, c.Params("id")) and writes the
// standard NotFound response (matching the convention already used across
// handlers) if the record can't be loaded. Callers should return the result
// directly, e.g.:
//
//	if err := utils.FindByID(c, h.DB, &company, "Company not found"); err != nil {
//		return err
//	}
func FindByID(c *fiber.Ctx, db *gorm.DB, dest interface{}, notFoundMsg string) error {
	if err := db.First(dest, c.Params("id")).Error; err != nil {
		// NotFound writes the response and returns whatever c.JSON(...)
		// returns, which is nil on a successful write (see ErrHandled's
		// doc in errors.go) — not the non-nil error our caller checks for.
		// Write it, then return a distinct non-nil error so the caller's
		// `if err != nil { return err }` actually stops the handler.
		_ = NotFound(c, notFoundMsg)
		return ErrHandled
	}
	return nil
}

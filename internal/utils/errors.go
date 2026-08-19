package utils

import (
	"errors"

	"github.com/gofiber/fiber/v2"
)

// ErrHandled is the sentinel a standalone validation helper (one that isn't
// itself the fiber.Handler — e.g. validateCompanyEmail, called from within
// UserHandler.Create) should return to signal "I already wrote the error
// response to c; stop processing." c.Status(...).JSON(...) itself returns nil
// on a successful write, so a helper that just forwarded ValidationError's
// return value would return nil even on the invalid path — the classic bug
// this sentinel exists to prevent: the caller's `if err != nil { return err }`
// guard would never trigger, validation would silently pass, and the 422 body
// already written would just get overwritten by whatever success response the
// handler produces afterward. Callers should propagate this as `return nil`
// (not `return err`) once detected — the response is already complete, and
// returning it further up would hand a non-nil error to Fiber's registered
// error handler, which would write a second, wrong response on top of it.
var ErrHandled = errors.New("validation response already written")

// ErrorResponse matches the envelope in api-system-spec.md §1.5.
func ErrorResponse(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    code,
			"message": message,
		},
	})
}

// ValidationError attaches the per-field `fields` map §1.5 says is present only
// on 422 responses.
func ValidationError(c *fiber.Ctx, message string, fields map[string][]string) error {
	return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    "VALIDATION_ERROR",
			"message": message,
			"fields":  fields,
		},
	})
}

func Unauthorized(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusUnauthorized, "UNAUTHORIZED", message)
}

func Forbidden(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusForbidden, "FORBIDDEN", message)
}

func NotFound(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusNotFound, "NOT_FOUND", message)
}

func BadRequest(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusBadRequest, "BAD_REQUEST", message)
}

func Internal(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", message)
}

func Conflict(c *fiber.Ctx, message string) error {
	return ErrorResponse(c, fiber.StatusConflict, "CONFLICT", message)
}

package utils

import "github.com/gofiber/fiber/v2"

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

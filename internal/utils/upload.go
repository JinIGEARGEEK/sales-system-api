package utils

import (
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const uploadDir = "./uploads"
const MaxUploadSize = 10 * 1024 * 1024

// ErrFileTooLarge is a sentinel so callers can use errors.Is instead of
// matching on err.Error() text, which silently breaks if this message changes.
var ErrFileTooLarge = errors.New("file too large")

// SaveUpload stores a multipart file under ./uploads (no real S3 wired up yet)
// and returns a durable-looking local file_url like /uploads/<filename>.
func SaveUpload(c *fiber.Ctx, fh *multipart.FileHeader) (fileURL string, size int64, err error) {
	if fh.Size > MaxUploadSize {
		return "", 0, ErrFileTooLarge
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return "", 0, err
	}

	ext := filepath.Ext(fh.Filename)
	name := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), uuid.NewString(), ext)
	dest := filepath.Join(uploadDir, name)

	if err := c.SaveFile(fh, dest); err != nil {
		return "", 0, err
	}
	return "/uploads/" + name, fh.Size, nil
}

// RespondUploadError maps a SaveUpload error to the right HTTP response,
// so callers don't each re-implement the ErrFileTooLarge check.
func RespondUploadError(c *fiber.Ctx, err error) error {
	if errors.Is(err, ErrFileTooLarge) {
		return ErrorResponse(c, fiber.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "File exceeds 10MB limit")
	}
	return Internal(c, "Failed to save file")
}

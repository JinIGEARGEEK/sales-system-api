package utils

import (
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

// SaveUpload stores a multipart file under ./uploads (no real S3 wired up yet)
// and returns a durable-looking local file_url like /uploads/<filename>.
func SaveUpload(c *fiber.Ctx, fh *multipart.FileHeader) (fileURL string, size int64, err error) {
	if fh.Size > MaxUploadSize {
		return "", 0, fmt.Errorf("file too large")
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

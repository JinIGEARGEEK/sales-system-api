package utils

import (
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// UploadDir is exported so routes.go can register a static file server over
// the same directory SaveUpload writes to — otherwise the /uploads/<name>
// URLs this returns are dead links (nothing ever served them).
const UploadDir = "./uploads"
const MaxUploadSize = 10 * 1024 * 1024

// ErrFileTooLarge is a sentinel so callers can use errors.Is instead of
// matching on err.Error() text, which silently breaks if this message changes.
var ErrFileTooLarge = errors.New("file too large")

// ErrUnsupportedFileType is returned when the upload's extension isn't on
// allowedUploadExts.
var ErrUnsupportedFileType = errors.New("unsupported file type")

// allowedUploadExts restricts uploads (Quote/Contract docs, Attachments) to
// document/image formats a browser won't execute. This matters once the file
// is actually served (see UploadDir/routes.go's Static registration) — an
// .html or .svg "attachment" served from this app's own origin would be a
// stored-XSS vector against anyone who opens the link; none of these
// extensions render as executable content.
var allowedUploadExts = map[string]bool{
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".csv": true,
}

// SaveUpload stores a multipart file under UploadDir (no real S3 wired up
// yet) and returns a durable-looking local file_url like /uploads/<filename>.
func SaveUpload(c *fiber.Ctx, fh *multipart.FileHeader) (fileURL string, size int64, err error) {
	if fh.Size > MaxUploadSize {
		return "", 0, ErrFileTooLarge
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowedUploadExts[ext] {
		return "", 0, ErrUnsupportedFileType
	}
	if err := os.MkdirAll(UploadDir, 0755); err != nil {
		return "", 0, err
	}

	name := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), uuid.NewString(), ext)
	dest := filepath.Join(UploadDir, name)

	if err := c.SaveFile(fh, dest); err != nil {
		return "", 0, err
	}
	return "/uploads/" + name, fh.Size, nil
}

// RespondUploadError maps a SaveUpload error to the right HTTP response,
// so callers don't each re-implement the ErrFileTooLarge/ErrUnsupportedFileType checks.
func RespondUploadError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, ErrFileTooLarge):
		return ErrorResponse(c, fiber.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "File exceeds 10MB limit")
	case errors.Is(err, ErrUnsupportedFileType):
		return BadRequest(c, "Unsupported file type")
	default:
		return Internal(c, "Failed to save file")
	}
}

package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// UploadDir is exported so LocalStorage's caller (routes.go, tests) can find
// the same directory Save writes to.
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
// is actually served (see the /uploads route) — an .html or .svg "attachment"
// served from this app's own origin would be a stored-XSS vector against
// anyone who opens the link; none of these extensions render as executable
// content.
var allowedUploadExts = map[string]bool{
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".csv": true,
}

// allowedContentTypesByExt maps each allowed extension to the sniffed
// content types (via http.DetectContentType against the file's first bytes)
// it's permitted to actually contain — closing the gap where the extension
// check above trusts the filename alone. Without this, a caller could
// rename a disallowed file (HTML, SVG, ...) to a permitted extension to
// slip past allowedUploadExts; since /uploads (routes.go) serves the file
// back with Content-Disposition: attachment (forcing a download rather than
// inline rendering), the practical risk this closes is narrower than a
// stored-XSS render, but it still stops a caller from stashing arbitrary
// (and mislabeled) content behind a trusted-looking extension.
//
// Deliberately not stricter than this: Go's stdlib sniffer has no OOXML- or
// legacy-CFB-specific signature, so .docx/.xlsx (zip-based) and .doc/.xls
// (binary OLE) all fall back to a generic "could be any binary blob" result
// (application/zip or application/octet-stream) that's allowed through here
// rather than rejected — validating further into those container formats
// would need a dedicated library this codebase doesn't otherwise depend on.
// What this DOES catch: an HTML/SVG/script payload (or a plain-text file)
// renamed to any of these extensions, and an image renamed to a document
// extension or vice versa.
var allowedContentTypesByExt = map[string]map[string]bool{
	".pdf":  {"application/pdf": true},
	".png":  {"image/png": true},
	".jpg":  {"image/jpeg": true},
	".jpeg": {"image/jpeg": true},
	".doc":  {"application/octet-stream": true, "application/x-cfb": true},
	".xls":  {"application/octet-stream": true, "application/x-cfb": true},
	".docx": {"application/zip": true, "application/octet-stream": true},
	".xlsx": {"application/zip": true, "application/octet-stream": true},
	".csv":  {"application/octet-stream": true, "text/plain; charset=utf-8": true},
}

// Storage abstracts where uploaded files (Quote PDFs, signed Contracts,
// Attachments) actually live — see biz_spec/s3-migration-plan.md for why:
// local disk on a stateless container platform is wiped on every redeploy
// and doesn't work past one replica. Handlers and the /uploads route only
// ever talk to this interface, never to os/S3 directly, so the backend is a
// config choice (STORAGE_BACKEND), not a code change.
type Storage interface {
	// Save validates fh (size/extension) and persists it, returning a stable
	// key — NOT a URL. Callers turn that into the "/uploads/<key>" file_url
	// they've always returned; the proxy-not-presigned design in the
	// migration plan keeps every download behind this API's own auth.
	Save(fh *multipart.FileHeader) (key string, size int64, err error)
	// Open streams the object identified by key back out, for the /uploads
	// route. Returns an error satisfying errors.Is(err, os.ErrNotExist) (or
	// equivalent) when the key doesn't exist.
	Open(key string) (io.ReadCloser, error)
}

func validateUpload(fh *multipart.FileHeader) (ext string, err error) {
	if fh.Size > MaxUploadSize {
		return "", ErrFileTooLarge
	}
	ext = strings.ToLower(filepath.Ext(fh.Filename))
	if !allowedUploadExts[ext] {
		return "", ErrUnsupportedFileType
	}
	if err := validateContentSniff(fh, ext); err != nil {
		return "", err
	}
	return ext, nil
}

// validateContentSniff rejects a file whose actual bytes (sniffed via
// http.DetectContentType, the same algorithm net/http uses to guess a
// response's Content-Type) don't match what ext claims to be — see
// allowedContentTypesByExt's own doc for exactly what this does and doesn't
// catch. The client-supplied fh.Header's Content-Type is never trusted for
// this; it's attacker-controlled the same as the filename/extension.
func validateContentSniff(fh *multipart.FileHeader, ext string) error {
	// validateUpload only ever calls this after confirming ext is a key of
	// allowedUploadExts, which allowedContentTypesByExt mirrors exactly —
	// so allowed is never the nil map from a missing key in practice. If
	// that precondition were ever violated, failing closed (nil map's zero
	// value makes every sniffed type "not allowed" below) is the safe
	// direction to get it wrong in, not silently skipping the check.
	allowed := allowedContentTypesByExt[ext]
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	// http.DetectContentType only ever looks at (up to) the first 512
	// bytes — reading less than that (a small file) is fine, and
	// io.ReadFull's ErrUnexpectedEOF/EOF for a short read just means "sniff
	// on however many bytes the file actually has."
	buf := make([]byte, 512)
	n, err := io.ReadFull(src, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	if !allowed[http.DetectContentType(buf[:n])] {
		return ErrUnsupportedFileType
	}
	return nil
}

func newUploadKey(ext string) string {
	return fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), uuid.NewString(), ext)
}

// LocalStorage writes to a directory on local disk — SaveUpload's original
// behavior, moved into a Storage implementation verbatim. Default backend
// for local dev and docker-compose (no bucket needed to run on a laptop);
// NOT durable in a deployment whose filesystem is ephemeral (Railway) or that
// runs more than one replica — see biz_spec/s3-migration-plan.md.
type LocalStorage struct {
	Dir string
}

func NewLocalStorage(dir string) *LocalStorage {
	return &LocalStorage{Dir: dir}
}

func (s *LocalStorage) Save(fh *multipart.FileHeader) (string, int64, error) {
	ext, err := validateUpload(fh)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(s.Dir, 0755); err != nil {
		return "", 0, err
	}
	key := newUploadKey(ext)
	src, err := fh.Open()
	if err != nil {
		return "", 0, err
	}
	defer src.Close()
	dest, err := os.Create(filepath.Join(s.Dir, key))
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = dest.Close() }()
	if _, err := io.Copy(dest, src); err != nil {
		return "", 0, err
	}
	return key, fh.Size, nil
}

func (s *LocalStorage) Open(key string) (io.ReadCloser, error) {
	// filepath.Base strips any directory component a malicious/malformed key
	// might carry (e.g. "../../etc/passwd") — keys are always generated by
	// newUploadKey and never contain a separator in practice, but Open is
	// reachable from a request path (routes.go), so this is defense in depth
	// against path traversal rather than a behavior change for real keys.
	return os.Open(filepath.Join(s.Dir, filepath.Base(key)))
}

// S3Storage stores objects in an S3-compatible bucket (AWS S3, Cloudflare
// R2, Backblaze B2, MinIO) — see biz_spec/s3-migration-plan.md. Selected via
// STORAGE_BACKEND=s3; config.Load's fail-fast checks ensure the required
// S3_* vars are present before this is ever constructed.
type S3Storage struct {
	client *s3.Client
	bucket string
}

// NewS3Storage builds an S3-compatible client. endpoint/forcePathStyle are
// only needed for non-AWS providers (R2/B2/MinIO) — leave endpoint empty and
// forcePathStyle false for real AWS S3.
func NewS3Storage(ctx context.Context, bucket, region, endpoint, accessKeyID, secretAccessKey string, forcePathStyle bool) (*S3Storage, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = forcePathStyle
	})
	return &S3Storage{client: client, bucket: bucket}, nil
}

func (s *S3Storage) Save(fh *multipart.FileHeader) (string, int64, error) {
	ext, err := validateUpload(fh)
	if err != nil {
		return "", 0, err
	}
	src, err := fh.Open()
	if err != nil {
		return "", 0, err
	}
	defer src.Close()

	key := newUploadKey(ext)
	_, err = s.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          src,
		ContentLength: aws.Int64(fh.Size),
	})
	if err != nil {
		return "", 0, err
	}
	return key, fh.Size, nil
}

func (s *S3Storage) Open(key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

// MemoryStorage is an in-memory Storage for tests — no real disk or bucket
// needed, so the integration suite doesn't depend on filesystem state
// surviving between runs. testutil.App uses this as the default.
type MemoryStorage struct {
	files map[string][]byte
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{files: make(map[string][]byte)}
}

func (s *MemoryStorage) Save(fh *multipart.FileHeader) (string, int64, error) {
	ext, err := validateUpload(fh)
	if err != nil {
		return "", 0, err
	}
	src, err := fh.Open()
	if err != nil {
		return "", 0, err
	}
	defer src.Close()
	data, err := io.ReadAll(src)
	if err != nil {
		return "", 0, err
	}
	key := newUploadKey(ext)
	s.files[key] = data
	return key, fh.Size, nil
}

func (s *MemoryStorage) Open(key string) (io.ReadCloser, error) {
	data, ok := s.files[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}

// RespondUploadError maps a Storage.Save error to the right HTTP response,
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

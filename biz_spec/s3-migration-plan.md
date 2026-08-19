# Object storage migration plan — local disk → S3-compatible

**Status: planned, not implemented.** This document is the design for moving
`Quote`/`Contract`/`Attachment` file storage off local disk. No code in this
repo implements it yet — see the "File uploads" step under "Deploying to
Railway" and the "Notes for contributors" section in [README.md](../README.md)
for the current (local-disk) behavior this replaces.

## Why

`internal/utils/upload.go`'s `SaveUpload` writes to `./uploads` on local disk
(resolves to `/app/uploads` in the Docker/Railway deploy). Two concrete
failure modes today:

1. **Not durable.** Railway's filesystem is ephemeral — every redeploy wipes
   `/app/uploads`. Every Quote PDF, signed Contract, and Attachment uploaded
   between deploys is lost.
2. **Doesn't scale past one replica.** A second instance has its own separate
   `/app/uploads` — an upload saved by instance A returns a 404 when instance
   B happens to serve the download request.

Neither is a bug introduced by this repo's code; they're the inherent limit
of "local disk on a stateless container platform." Object storage removes
both.

## Current call sites (what has to change)

Deliberately already centralized — this migration touches 4 files, not the
whole codebase:

| File | What it does today |
|---|---|
| `internal/utils/upload.go` | `SaveUpload(c, fh) (fileURL string, size int64, err error)` — validates size/extension, writes to disk, returns `/uploads/<name>`. `RespondUploadError` maps its errors to HTTP responses. |
| `internal/routes/routes.go:90` | `app.Static("/uploads", utils.UploadDir, ...)` — serves the directory, auth-gated via `app.Use("/uploads", middleware.RequireAuth(cfg))`. |
| `internal/handlers/attachments.go` | Calls `SaveUpload` for `POST /attachments` (multipart branch). |
| `internal/handlers/quotes.go` | Calls `SaveUpload` for `POST /deals/:dealId/quotes/upload`. |
| `internal/handlers/contracts.go` | Calls `SaveUpload` for `POST /contracts/:id/upload`. |

`Quote.FileURL`, `Contract.SignedFileURL`, and `Attachment.FileURL` are all
plain `*string` columns holding whatever `SaveUpload` returned — no schema
change is required by this plan (see "URL/serving strategy" below for why).

## Design: a `Storage` interface

Replace `SaveUpload`'s direct `os` calls with a small interface, so the
handlers and the `/uploads` route never know which backend is behind them:

```go
// internal/utils/storage.go
type Storage interface {
    // Save validates and persists fh, returning a stable key (NOT a URL —
    // see "URL/serving strategy"). Same validation rules SaveUpload has
    // today: MaxUploadSize, allowedUploadExts.
    Save(fh *multipart.FileHeader) (key string, size int64, err error)
    // Open streams the object back out by key, for the download route.
    Open(key string) (io.ReadCloser, error)
}
```

Two implementations:

- **`LocalStorage`** — wraps today's `os.MkdirAll`/`SaveFile`/`os.Open` logic
  verbatim. Stays the default for local dev and `docker-compose` (no bucket
  needed to run the app on a laptop).
- **`S3Storage`** — backed by [`aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2)'s
  S3 client, configured with a custom endpoint so it works against real AWS
  S3 **or** any S3-compatible provider (Cloudflare R2, Backblaze B2, MinIO for
  CI — see "Testing strategy"). `Save` does a `PutObject`, `Open` does a
  `GetObject` and returns its `Body` (already an `io.ReadCloser`).

`config.Load()` picks one at startup based on `STORAGE_BACKEND` (see below)
and hands it to `routes.Setup` alongside `db`/`cfg`, same as any other
dependency — handlers stop calling `utils.SaveUpload` as a package function
and call `h.Storage.Save(fh)` on an injected instance instead (small,
mechanical signature change at the 3 call sites above).

## URL/serving strategy: keep the proxy, don't switch to presigned URLs (yet)

Two ways to let the frontend actually download a file once it's in S3:

1. **Presigned URLs** — `Save`/a follow-up call generates a time-limited S3
   URL, store *that* in `FileURL`, frontend fetches S3 directly.
2. **Proxy through the API** (recommended for phase 1) — keep the exact
   `/uploads/<key>` URL shape and auth gate that exist today; the route
   handler calls `Storage.Open(key)` and streams the bytes through, same as
   `app.Static` does for local disk now.

**Recommendation: proxy, not presigned, for the initial migration.** Reasons:

- **Zero contract change.** `file_url` keeps meaning exactly what it means
  today — a path this API serves, auth-gated the same way. The frontend
  needs no changes at all.
- **Auth stays centralized.** A presigned URL is bearer-token-equivalent for
  its lifetime — anyone who has the link can download it, no
  `RequireAuth`/RBAC check in the loop. The proxy keeps every download going
  through this API's existing auth middleware, matching how every other
  endpoint in this system works (NFR-001, "RBAC enforced server-side on
  every route").
- **Simpler ops.** No bucket CORS policy, no clock-skew-sensitive signature
  expiry to debug, no worrying about a presigned link being cached/shared
  past its intended lifetime.

Tradeoff being accepted: the API's egress bandwidth doubles for downloads
(S3 → API → client, instead of S3 → client directly). At this system's
documented scale (NFR-003: ~10,000 records, not a media-heavy app) this is
a non-issue. Revisit if per-file size or download volume ever becomes large
enough that egress cost/latency matters — the `Storage` interface makes
swapping to presigned URLs later a contained change (a second interface
method, `URL(key string) (string, error)`), not a rewrite.

## Config

New `Config` fields (`internal/config/config.go`), following the existing
SMTP optional-feature pattern (empty/absent = safe default, not a crash):

| Var | Default | Purpose |
|---|---|---|
| `STORAGE_BACKEND` | `local` | `local` or `s3`. |
| `S3_BUCKET` | `""` | Required when `STORAGE_BACKEND=s3`. |
| `S3_REGION` | `""` | Required when `STORAGE_BACKEND=s3`. |
| `S3_ENDPOINT` | `""` | Optional custom endpoint — set for R2/B2/MinIO; leave empty for real AWS S3. |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | `""` | Required when `STORAGE_BACKEND=s3` and not relying on an IAM instance role (Railway has none, so these will always be needed for this deployment target). |
| `S3_FORCE_PATH_STYLE` | `false` | Most S3-compatible providers (R2, B2, MinIO) need `true`; real AWS S3 needs `false`. |

**Fail-fast validation**, mirroring the `JWT_SECRET`/`CORS_ORIGINS`
deny-by-default checks already in `cmd/api/main.go`:

- `STORAGE_BACKEND=s3` with any required `S3_*` var missing → `log.Fatal` at
  boot, not a runtime 500 on the first upload attempt.
- Outside `development`, `STORAGE_BACKEND=local` should itself be a hard
  failure — local disk is the known-non-durable option, and we already treat
  "silently running with the unsafe default in production" as a boot-time
  error for `JWT_SECRET`/`CORS_ORIGINS`. Same reasoning applies here once S3
  is actually wired up: a production deploy that's quietly still on local
  disk is exactly the durability bug this migration exists to close.

## Migration steps (in order)

1. **Add `internal/utils/storage.go`** — the `Storage` interface,
   `LocalStorage`, and `S3Storage` implementations. `LocalStorage` is
   `SaveUpload`'s existing logic moved into a type, not rewritten — low risk.
2. **Add `aws-sdk-go-v2` + `aws-sdk-go-v2/service/s3` to `go.mod`.**
3. **Extend `config.Config`/`config.Load()`** with the `STORAGE_BACKEND`/`S3_*`
   fields and the fail-fast checks above.
4. **Wire a `Storage` instance through `cmd/api/main.go` → `routes.Setup` →
   the 3 handlers** (`AttachmentHandler`, `QuoteHandler`, `ContractHandler`
   each gain a `Storage` field, set in their `New*Handler` constructors —
   same pattern `DB *gorm.DB` already uses).
5. **Replace `app.Static("/uploads", ...)`** in `routes.go` with a handler
   that extracts the key from the URL, calls `Storage.Open(key)`, and streams
   the result — still behind the same `app.Use("/uploads",
   middleware.RequireAuth(cfg))` gate.
6. **Delete `utils.SaveUpload`/`RespondUploadError`** as free functions once
   nothing calls them (or keep `RespondUploadError`'s error-mapping logic,
   since it's backend-agnostic — only the save/open mechanics differ).
7. **Existing local files**: given Railway's filesystem is already wiped on
   every redeploy, there is almost certainly nothing on production disk worth
   migrating by the time this ships — confirm that assumption before writing
   a one-off backfill script, rather than building one speculatively.

## Testing strategy

- **Unit-level**: a third `Storage` implementation, `MemoryStorage` (an
  `io.ReadCloser`-backed `map[string][]byte`), satisfies the interface with
  no real I/O — use it as the default in `internal/testutil` so the existing
  integration suite (`tests/upload_serving_test.go` et al.) doesn't need real
  disk or a real bucket to pass.
- **Integration-level (optional, higher confidence)**: add a `minio/minio`
  service container to `.github/workflows/ci.yml` (same pattern as the
  existing `postgres` service) and one dedicated test that runs `S3Storage`
  against it with `S3_ENDPOINT` pointed at the container — proves the actual
  AWS SDK code path works, not just the interface contract.
- Either way, `docker-compose.yml` for local dev should default to
  `STORAGE_BACKEND=local` (no bucket needed to run the app on a laptop) with
  the S3 path opt-in via `.env`.

## Open decisions (need your input before implementation starts)

1. **Which provider?** AWS S3 (most standard, most expensive), Cloudflare R2
   (S3-compatible, no egress fees — worth it if we ever do move to presigned
   URLs later), or Backblaze B2 (S3-compatible, cheapest storage cost). The
   `S3_ENDPOINT`/`S3_FORCE_PATH_STYLE` design above works with any of the
   three without code changes — this is purely an account-setup decision.
2. **Bucket naming/region** and who provisions it (you, via each provider's
   console/CLI — not something I can do from here).
3. **Retention/lifecycle policy** — e.g. auto-expire old Quote PDFs after N
   years, or keep everything indefinitely. Not required for phase 1, but
   worth deciding since it's a bucket-policy setting, not application code.
4. **Confirm the "nothing on production disk is worth migrating" assumption**
   in "Migration steps" §7 above before that step is treated as a no-op.

## Effort estimate

Steps 1–6 are a contained, mechanical change (~1 day of implementation +
review) given the existing call sites are already centralized to 3 handlers.
Step 7 (backfill) and the CI MinIO integration test are each small
additions on top if wanted. The larger unknown is entirely outside code: an
S3-compatible account, bucket, and IAM credentials need to exist first
(open decisions #1–#2 above) — implementation can start the moment those are
available.

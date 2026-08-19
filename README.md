# Sales System API

Backend API for the **I GEAR GEEK Sales CRM** — powers the [`sales-system`](https://bitbucket.org/i-gear-geek/sales-system) Nuxt 3 frontend, replacing its client-side mock data resource by resource.

Implements the contract defined in [`biz_spec/api-system-spec.md`](biz_spec/api-system-spec.md), which is the source of truth for every route, request/response shape, and role.

## Tech stack

- **Go 1.25** + [Fiber v2](https://gofiber.io/) (HTTP framework)
- **GORM** + `gorm.io/driver/postgres` (ORM)
- **PostgreSQL** (database)
- **golang-jwt/jwt/v5** (auth) + bcrypt (password hashing)

`go.mod`'s `go` directive pins a specific patch version (not just `1.25`) deliberately — Go's `GOTOOLCHAIN=auto` (default since 1.21) then auto-fetches that exact patch for anyone building the repo, local or CI, which is how stdlib security fixes actually reach every environment without each one needing to separately upgrade its installed `go` binary. Bump it whenever `govulncheck` (run in CI, see below) reports a fixed-in version ahead of what's pinned.

## Project layout

```
cmd/api/            entrypoint — config load, DB connect/migrate, admin seed, Fiber app
internal/
  config/           env var loading
  database/         GORM connection + AutoMigrate
  models/           GORM models, one file per resource
  middleware/       JWT auth + RBAC middleware
  handlers/         one file per resource, HTTP handlers
  routes/           route registration (/api/v1/...)
  utils/            response envelopes, pagination, JWT, password hashing, uploads
  testutil/         test harness (isolated test DB + in-process Fiber app)
tests/              integration test suite (black-box, via internal/testutil)
biz_spec/           api-system-spec.md — the API contract this repo implements
```

## Getting started

### Prerequisites

- Go 1.25+
- PostgreSQL (locally via Homebrew, or `docker-compose up -d` using the provided `docker-compose.yml`)

### Setup

```sh
cp .env.example .env      # adjust DB_* / JWT_SECRET as needed
go run ./cmd/api
```

On first run, if the `users` table is empty, the server seeds an Admin account and logs its generated email/password to stdout — use that to log in and start creating data.

Migrations run automatically on boot via `database.AutoMigrate` — no separate migration step needed for local dev.

### Environment variables

See [`.env.example`](.env.example). Notable ones:

| Var | Purpose |
|---|---|
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | Postgres connection |
| `JWT_SECRET` | HMAC secret for signing tokens — **must** be overridden outside local dev; the server refuses to boot with the default value whenever `APP_ENV` is anything other than `development` (deny-by-default — a misspelled/unset `APP_ENV` fails closed instead of silently booting with a guessable secret) |
| `JWT_EXPIRY_HOURS` | Access token lifetime |
| `CORS_ORIGINS` | Comma-separated allow-list of origins. Defaults to `*` (any origin) for local dev; the server refuses to boot with `*` whenever `APP_ENV` is anything other than `development`, same deny-by-default reasoning as `JWT_SECRET` above — set an explicit allow-list in every other environment |
| `PORT` | HTTP listen port (default `8080`) |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | Outbound mail server for the Task due-date reminder emails. Optional — if `SMTP_HOST` is left unset, the mailer logs a warning and skips sending instead of failing, so the app runs fine without these configured. Set all five in production to actually deliver reminder emails. |

### Task due-date reminders

A background goroutine (`internal/notifier`) polls every 15 minutes for pending Tasks whose `due_date` has passed and haven't been notified yet, and emails the assignee (via `SMTP_*` above) with the task title, due date, and related Deal/Contact/Company name when resolvable. Each task is marked with `notified_at` after sending so the reminder only goes out once. A failed send (or missing SMTP config) is logged and does not block other tasks' reminders.

## Testing

```sh
go test ./...
```

Tests run against a separate `sales_system_test` database (created automatically if missing) via an in-process Fiber app — no real network listener, and the shared dev database is never touched. Coverage focuses on auth, RBAC/ownership enforcement, and the specific data-integrity behaviors called out in the spec (partial updates not clobbering omitted fields, soft- vs hard-delete semantics, audit logging on sensitive actions). CI also runs `-race` and a `govulncheck` job — see the note on `go.mod`'s pinned patch version above.

## API overview

All routes are prefixed `/api/v1`. Auth is a Bearer JWT (`Authorization: Bearer <token>`), obtained via `POST /auth/login`.

Resources: Auth & Users, Leads, Companies, Contacts, Deals, Activities, Tags, Quotes, Payments, Tasks, Contracts, Products & Customer-Products, Projects, Reports, Audit log, Dashboard aggregate. See `biz_spec/api-system-spec.md` for the full endpoint list, request/response shapes, filters, and per-endpoint status (🟢 required / 🔜 planned).

`POST /auth/login` is rate-limited to 10 attempts/minute per client IP (resolved from `X-Forwarded-For` behind Railway's proxy, falling back to the raw connection address for local/direct connections) — see `internal/routes/routes.go`.

### Roles (§1.7 of the spec)

| Role | Access |
|---|---|
| **Admin** | Full access to every resource, including Users and Product Catalog |
| **Sales Rep** | Full CRUD on records assigned to them or unassigned; read access to teammates' records |
| **Sales Manager** | Same as Sales Rep, plus read access to all reps' data, all `/reports/*`, and deal reassignment |
| **Production** | Write access to *only* `status` and `production_reference` on `Project` records |

RBAC is enforced server-side on every route — never rely on the frontend hiding a button (NFR-001).

## Deploying to Railway

The repo builds via the included `Dockerfile` and `railway.toml` (health check at `GET /health`, restart-on-failure). Steps:

1. **Create the Railway project** and add a **PostgreSQL** plugin — Railway injects `DATABASE_URL` automatically, which `internal/config`/`internal/database` prefer over the discrete `DB_*` vars, so no extra DB wiring is needed.
2. **Connect the repo.** Railway's native git auto-deploy only supports GitHub — this repo lives on Bitbucket, so either:
   - Mirror the repo to GitHub and connect that, or
   - Deploy via the [Railway CLI](https://docs.railway.app/guides/cli) (`railway login && railway link && railway up`) run locally or from a Bitbucket Pipeline (`railway up --service <name>` using a `RAILWAY_TOKEN` repo variable) on every push to `main`.
3. **Set environment variables** on the service (Railway dashboard → Variables): `JWT_SECRET` (required — a real secret, not the default), `JWT_EXPIRY_HOURS`, `APP_ENV=production`. Leave `PORT` unset — Railway injects it and `config.Load()` already reads it.
4. **File uploads**: `./uploads` (Quote PDFs, signed Contracts, Attachments) is local-disk storage, served back at `/uploads/<name>` (auth-required — any authenticated role) via `app.Static` in `internal/routes/routes.go`. It does **not** persist across redeploys or scale across replicas on Railway's ephemeral filesystem. Either:
   - Add a [Railway Volume](https://docs.railway.app/reference/volumes) mounted at `/app/uploads` as a quick fix (fine for a single instance), or
   - Move to S3-compatible object storage (the real fix, needed before this handles production traffic at any scale) — planned but not implemented yet, see [`biz_spec/s3-migration-plan.md`](biz_spec/s3-migration-plan.md) for the design (storage backend, config, migration steps, open decisions blocking implementation).
5. **First deploy**: the app auto-runs `AutoMigrate` and seeds an initial Admin account on boot if `users` is empty — check the deploy logs for the generated email/password.
6. **Frontend**: the `sales-system` Nuxt app builds to a static SPA (`ssr: false`) — Railway can serve it too (small Dockerfile + static file server, or Nixpacks auto-detection), but S3+CloudFront/Vercel/Netlify are typically simpler/cheaper for a pure static build. Whichever host you pick, set its `API_URL` build-time env var to this service's Railway-issued domain (or custom domain once attached).

## Notes for contributors

- The frontend's `AdminUser.role` type was originally `Admin | Editor | Viewer`, which conflicted with the roles actually enforced here. This has been reconciled: both frontend and backend now use `Admin | Sales Rep | Sales Manager | Production`.
- File uploads (Quote PDFs, signed Contracts, Attachments) are currently stored on local disk under `./uploads` and served back auth-gated at `/uploads/<name>` — swap for S3-compatible object storage before any real deployment. See [`biz_spec/s3-migration-plan.md`](biz_spec/s3-migration-plan.md) for the planned design (a `Storage` interface with `Local`/`S3` implementations, config, migration steps, and the open decisions — provider choice, bucket setup — blocking implementation). Accepted extensions are allow-listed (`.pdf .png .jpg .jpeg .doc .docx .xls .xlsx .csv`) in `internal/utils/upload.go` — deliberately excludes anything a browser would execute inline, since files are served from this API's own origin.
- `AutoMigrate` runs on every boot; fine for dev, but consider gating it behind a flag or a separate migration step before running multiple replicas in production. It also backfills the `companies.domain` column (used for indexed import dedup) for any pre-existing row missing it — idempotent, only touches rows where `domain` is still empty, so it's a no-op after the first boot post-upgrade.
- `GET /{resource}/export` streams its CSV response in batches rather than buffering the full result set in memory — safe for large tables, but note that once the first batch has been validated and streaming begins, a failure partway through can only be logged server-side and cut the response short (the `200` and any bytes already sent can't be un-sent); this is an inherent limitation of streamed HTTP responses.
- Two handlers cache in an in-process map for correctness/performance (`internal/middleware`'s `must_change_password` cache and `internal/handlers/dashboard.go`'s `GET /dashboard/summary` cache, both with short TTLs and explicit invalidation on writes within the same process). Fine for a single Railway replica; running multiple replicas would need these moved to a shared store (e.g. Redis) since each replica's cache is currently independent and can briefly disagree with the others.

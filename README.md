# Sales System API

Backend API for the **I GEAR GEEK Sales CRM** — powers the [`sales-system`](https://bitbucket.org/i-gear-geek/sales-system) Nuxt 3 frontend, replacing its client-side mock data resource by resource.

Implements the contract defined in [`biz_spec/api-system-spec.md`](biz_spec/api-system-spec.md), which is the source of truth for every route, request/response shape, and role.

## Tech stack

- **Go 1.25** + [Fiber v2](https://gofiber.io/) (HTTP framework)
- **GORM** + `gorm.io/driver/postgres` (ORM)
- **PostgreSQL** (database)
- **golang-jwt/jwt/v5** (auth) + bcrypt (password hashing)

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

On first run, if the `users` table is empty, the server seeds an Admin account and logs its generated username/password to stdout — use that to log in and start creating data.

Migrations run automatically on boot via `database.AutoMigrate` — no separate migration step needed for local dev.

### Environment variables

See [`.env.example`](.env.example). Notable ones:

| Var | Purpose |
|---|---|
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | Postgres connection |
| `JWT_SECRET` | HMAC secret for signing tokens — **must** be overridden in production; the server refuses to boot with the default value when `APP_ENV=production` |
| `JWT_EXPIRY_HOURS` | Access token lifetime |
| `PORT` | HTTP listen port (default `8080`) |

## Testing

```sh
go test ./...
```

Tests run against a separate `sales_system_test` database (created automatically if missing) via an in-process Fiber app — no real network listener, and the shared dev database is never touched. Coverage focuses on auth, RBAC/ownership enforcement, and the specific data-integrity behaviors called out in the spec (partial updates not clobbering omitted fields, soft- vs hard-delete semantics, audit logging on sensitive actions).

## API overview

All routes are prefixed `/api/v1`. Auth is a Bearer JWT (`Authorization: Bearer <token>`), obtained via `POST /auth/login`.

Resources: Auth & Users, Leads, Companies, Contacts, Deals, Activities, Tags, Quotes, Payments, Tasks, Contracts, Products & Customer-Products, Projects, Reports, Audit log, Dashboard aggregate. See `biz_spec/api-system-spec.md` for the full endpoint list, request/response shapes, filters, and per-endpoint status (🟢 required / 🔜 planned).

### Roles (§1.7 of the spec)

| Role | Access |
|---|---|
| **Admin** | Full access to every resource, including Users and Product Catalog |
| **Sales Rep** | Full CRUD on records assigned to them or unassigned; read access to teammates' records |
| **Sales Manager** | Same as Sales Rep, plus read access to all reps' data, all `/reports/*`, and deal reassignment |
| **Production** | Write access to *only* `status` and `production_reference` on `Project` records |

RBAC is enforced server-side on every route — never rely on the frontend hiding a button (NFR-001).

## Notes for contributors

- The frontend's `AdminUser.role` type was originally `Admin | Editor | Viewer`, which conflicted with the roles actually enforced here. This has been reconciled: both frontend and backend now use `Admin | Sales Rep | Sales Manager | Production`.
- File uploads (Quote PDFs, signed Contracts) are currently stored on local disk under `./uploads` — swap for S3-compatible object storage before any real deployment.
- `AutoMigrate` runs on every boot; fine for dev, but consider gating it behind a flag or a separate migration step before running multiple replicas in production.

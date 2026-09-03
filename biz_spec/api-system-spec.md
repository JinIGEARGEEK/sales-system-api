# API System Specification — I GEAR GEEK Sales CRM

**Companion document to:** `feature-spec.md` (business requirements), `user-story.md` (role acceptance criteria), `design-system.md` (frontend conventions)
**Purpose:** The contract for this backend API. This document was originally written when the frontend (`sales-system`) was still 100% client-side mock data with no real backend at all — that's no longer the state of either repo (20+ merged PRs, a working Go/Fiber API, and a frontend wired up against it resource by resource). It's kept up to date as a living reference for the current contract rather than as a forward-looking build spec.
**Audience:** Backend/frontend engineers (and AI coding agents) working against this API.
**Version:** 1.3 (Prospect entity — pre-Lead marketing funnel, Marketing role — see `CHANGELOG.md`)
**Date:** 2026-09-01

> **Status legend** (mirrors `feature-spec.md`'s legend, applied per endpoint):
> 🟢 **Required now** — replaces an existing mock Pinia store; needed to take this frontend off mock data as-is.
> 🔜 **Planned** — supports a `feature-spec.md` requirement that isn't built in the frontend yet either (Quotes CRUD, Contracts, Products/Projects, RBAC enforcement, audit log). Build after the 🟢 set; the frontend will grow into these.

---

## บทบาทผู้ใช้และกรณีการใช้งาน (สรุปภาษาไทย)

> สรุปสั้น ๆ สำหรับทีม Backend ก่อนเข้าสู่รายละเอียดสัญญา API ด้านล่าง — ดูรายละเอียดเต็มที่ `feature-spec.md` และ `user-story.md`

### บทบาทผู้ใช้ (ใช้กำหนดสิทธิ์ระดับ API — ดูรายละเอียดที่ §1.7)

| บทบาท | สิทธิ์การเข้าถึง API โดยสรุป |
|---|---|
| **แอดมิน (Admin)** | เข้าถึงได้ทุก Resource รวมถึง Users, Tags และ (เมื่อสร้างแล้ว) Product Catalog / การตั้งค่า Pipeline |
| **เซลล์ / ผู้ดูแลลูกค้า (Sales Rep / Account Manager)** | CRUD เต็มรูปแบบบน Leads/Companies/Contacts/Deals/Activities/Tasks/Quotes/Payments ที่ตนรับผิดชอบหรือยังไม่มีผู้รับผิดชอบ อ่านข้อมูลของเพื่อนร่วมทีมได้ |
| **หัวหน้าทีมขาย (Sales Manager)** | เหมือนเซลล์ แต่เพิ่มสิทธิ์อ่านข้อมูลของทุกคนในทีมและทุก endpoint ใน `/reports/*` รวมถึงการโยกย้าย Deal |
| **ทีม Production (สิทธิ์จำกัด)** | เขียนได้เฉพาะ `status` และ `production_reference` ของ `Project` เท่านั้น (§8.3) — ไม่มีสิทธิ์เข้าถึง Resource อื่นใดเลย |
| **ทีม Marketing** | เพิ่มเมื่อ 2026-09-01 สำหรับ Prospect (§3a) — CRUD เต็มรูปแบบบน Prospects ที่ตนรับผิดชอบหรือยังไม่มีผู้รับผิดชอบ ไม่มีสิทธิ์เข้าถึง Leads/Deals หรือ Resource อื่น |

### กรณีการใช้งานที่ผูกกับ Endpoint จริง (ตัวอย่าง)

- **เซลล์ปิด Deal สำเร็จ** → เรียก `PATCH /deals/:id/stage` ด้วย `{ stage: 'Won' }` → ฝั่ง frontend สร้างงานติดตาม (Task) อัตโนมัติผ่าน `POST /tasks` ทันที (ดู §7.6) โดยยังไม่ผูกกับ CustomerProduct เนื่องจาก Product Catalog ยังไม่ถูกสร้าง (§8.2)
- **เซลล์ดูงานติดตามทั้งหมดของตน** → เรียก `GET /tasks?status=pending` (ไม่ระบุ `related_type`/`related_id`) เพื่อรวมงานจากทุก Deal/Contact/Company ในหน้าเดียว (§7.6) — endpoint เดียวกันนี้ยังขับเคลื่อนวิดเจ็ต "Upcoming Follow-ups" บน Dashboard (§9)
- **หัวหน้าทีมดูภาพรวมทีม** → เรียก `GET /dashboard/summary` พร้อม query filter ตามช่วงเวลา/Business Unit/Channel (§9) แทนที่จะดึงข้อมูลดิบทั้งหมดมาคำนวณฝั่ง frontend

---

## 1. Conventions

These apply to every endpoint below unless a section says otherwise.

### 1.1 Base URL & versioning

- Base URL comes from a single env var the frontend already reads: `API_URL` (see `nuxtApp.$config.public.API_URL` in `plugins/axios.ts`). No hardcoded host anywhere in the frontend.
- Prefix all routes with `/api/v1` (not yet reflected in the frontend's config value, but assumed by this spec so the backend can version breaking changes later without touching every consumer).
- JSON only. `Content-Type: application/json` for all requests except file uploads (§6), which use `multipart/form-data`.

### 1.2 Authentication

- Bearer JWT in the `Authorization` header: `Authorization: Bearer <access_token>`. The frontend already attaches this on every request via an axios interceptor (`plugins/axios.ts`) reading the token from `localStorage` (`useAuth` composable, `composables/utils/useAuth.ts`).
- The frontend's response interceptor already special-cases three status codes — the backend must return exactly these for the redirect logic in `plugins/axios.ts` to work:

| Status | Frontend behavior | When to return it |
|---|---|---|
| `401` | Redirects to `/login` | Missing/expired/invalid token |
| `403` | Redirects to `/` | Authenticated but not authorized for this resource/action |
| `404` | Redirects to `/error404` | Resource not found |

> **Forced password change is a special `403`.** Every route except `/auth/me`, `/auth/logout`, and `/auth/change-password` returns `403` with `error.code: "PASSWORD_CHANGE_REQUIRED"` for an account whose `must_change_password` is still `true` (§2.1) — set whenever an Admin creates the account or resets its password (§2.2), cleared only by a successful `POST /auth/change-password`. The frontend's blanket "`403` → redirect to `/`" rule (row above) needs a carve-out for this code — otherwise the account bounces to `/`, which immediately 403s again on its own API calls. Check `error.code` in the axios response interceptor and route to a dedicated "set your password" page instead when it's `PASSWORD_CHANGE_REQUIRED`.

- No refresh-token flow exists in the frontend today. Recommend a long-lived access token (or add refresh in the API without requiring a frontend change for v1 — `useAuth` would need a follow-up change to consume it).
- `POST /auth/login` is rate-limited to 10 attempts/minute per client IP (keyed off `X-Forwarded-For` behind Railway's proxy, falling back to the connection's own address) — a 429 with the standard error envelope (§1.5, `error.code: "TOO_MANY_REQUESTS"`) once exceeded. Not one of the three special-cased codes above; the frontend's default axios error handling (show the message, don't redirect) already covers it.

#### Endpoints

| Method | Path | Auth | Status | Description |
|---|---|---|---|---|
| `POST` | `/auth/login` | none | 🟢 | Body: `{ email: string, password: string }` (frontend field names, see `pages/login.vue`). Returns `{ access_token: string, user: User }`. Rate-limited — see above. |
| `POST` | `/auth/logout` | Bearer | 🟢 | Invalidates the token server-side by bumping `User.token_version` — every token issued to this user before now (not just the one used to call this) fails the next request's auth check. Frontend also clears `localStorage` regardless (`useAuth().removeAccessToken()`). A User being deactivated has the same effect, checked on every request. |
| `GET` | `/auth/me` | Bearer | 🟢 | Returns the current `User` (§2.1). Used to hydrate `stores/user.ts` on load instead of trusting client state alone. |
| `POST` | `/auth/change-password` | Bearer | 🟢 | Body: `{ current_password: string, new_password: string, confirm_password: string }`. Verifies `current_password`, requires `new_password === confirm_password` and at least 8 characters and different from the current password, then clears `must_change_password`. Returns the updated `User`. This is the only way to satisfy a `PASSWORD_CHANGE_REQUIRED` block (see the note above the status table), so it stays reachable even while that block is active. |

### 1.3 Response envelope

Two shapes, matching the frontend's existing `ApiResponse<T>` type (`interfaces/api.d.ts`) and the plain single-record pattern it implies:

**List responses** (paginated):

```json
{
  "data": [ /* T[] */ ],
  "page": 1,
  "per_page": 20,
  "total": 134,
  "total_page": 7,
  "next": 2,
  "prev": null
}
```

`next`/`prev` are `null` when there is no next/previous page (not omitted — the frontend's `TableData`/`TablePagination` components check for presence, not truthiness-only nullability edge cases).

**Single-record responses** (get one / create / update):

```json
{ "data": { /* T */ } }
```

**Delete**: `204 No Content`, empty body.

### 1.4 Pagination & filtering query params

All list endpoints accept:

| Param | Type | Default | Notes |
|---|---|---|---|
| `page` | number | `1` | 1-indexed, matches `TableData`'s `v-model:page` |
| `per_page` | number | `20` | Matches `useTablePagination` composable's default page size |
| `search` | string | — | Free-text match; each resource section below lists which fields it searches |
| `sort` | string | resource-specific | e.g. `sort=-created_at` (leading `-` = descending) |

Resource-specific filter params (status, stage, date range, etc.) are listed per section.

### 1.5 Error response shape

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Human-readable summary",
    "fields": { "email": ["Email already in use"] }
  }
}
```

`fields` is present only for `422` validation errors and keys by the same field names used in request bodies (snake_case, matching the TS interfaces below) so the frontend's Vee-Validate error display (`components/Input/FormField.vue`) can map them directly onto the right input.

### 1.6 Field & entity conventions

Every resource shares these unless noted:

- **`id`**: number, server-generated, auto-increment or equivalent. The frontend never generates IDs client-side for real records (mock stores use a local `nextId()` helper purely as a stand-in — see `stores/helpers.ts` — real IDs always come from the API).
- **Timestamps**: ISO 8601 strings in transit (`2026-08-14T09:30:00Z`); the frontend parses them to `Date` at the store boundary. Every entity has `created_at`; most have `updated_at`.
- **Soft delete + audit trail**: the base `User` shape (`interfaces/auth.d.ts`) already models `created_at/updated_at/deleted_at` + `created_by/updated_by/deleted_by` (user IDs). Apply the same pattern to every resource below — "delete" on Company/Contact/Tag/Product is `status: 'archived'` or `deleted_at` being set, never a hard row delete, so history and audit-log (§9) stay intact.
- **Enums**: transmitted as their literal string values (e.g. `"Qualified"`, not an integer code) — see each resource's TypeScript type for the exact allowed values, taken verbatim from `interfaces/crm.d.ts`.

### 1.7 Roles & authorization

Per `feature-spec.md` §2.2 / `user-story.md`. `FR-CRM-080` (RBAC enforcement) is 🔜 **Planned** — not enforced anywhere in the frontend today (`role` is a display-only field) — but the API must enforce it from day one; UI-only hiding is explicitly called out as insufficient (`NFR-001`).

| Role | Summary |
|---|---|
| **Admin** | Full access to every resource, including Users, Tags, and (once built) Product Catalog / pipeline config |
| **Sales Rep / Account Manager** | Full CRUD on Leads/Companies/Contacts/Deals/Activities/Tasks/Quotes/Payments they're assigned to or that are unassigned; read access to teammates' records |
| **Sales Manager** | Same as Sales Rep, plus read access to all reps' data and all `/reports/*` endpoints, plus deal reassignment |
| **Production (limited)** | Write access to *only* `status` and `production_reference` on `Project` records (§8.3) — no access to any other resource |
| **Marketing** | Added 2026-09-01 for the Prospect funnel (§3a) — full CRUD on Prospects they're assigned to or that are unassigned, same ownership model as Sales Rep has for Leads. No access to Leads/Deals/any other resource; Admin and Sales Manager retain oversight access to `/prospects` alongside Marketing. Not part of the original `feature-spec.md` §2.2 role table — see `internal/models/user.go`'s `RoleMarketing` doc comment. |

Suggested enforcement: role on the JWT claims, checked server-side per route — not by trusting a client-sent role header.

---

## 2. Auth & Users

### 2.1 `User` (base) / `AdminUser` (staff account)

Mirrors `interfaces/auth.d.ts` + `interfaces/admin.d.ts`:

```ts
interface User {
  id: number
  first_name: string
  last_name: string
  tel: string
  email: string   // login identifier; must be unique and on the @igeargeek.com domain
  accepted_consent_id: number | null
  is_active: boolean
  must_change_password: boolean   // true until the holder sets their own password via POST /auth/change-password
  latest_login: string | null   // ISO 8601
  created_at: string | null
  updated_at: string | null
  deleted_at: string | null
  created_by: number
  updated_by: number
  deleted_by: number
}

interface AdminUser extends User {
  role: 'Admin' | 'Editor' | 'Viewer'
}
```

> Note: `feature-spec.md` §2.2 names three business roles (Admin, Sales Rep, Sales Manager) but the built `AdminUser.role` enum today is `Admin | Editor | Viewer` (from `pages/admin/users/`'s `ROLE_OPTIONS`). Reconcile this naming mismatch with the product owner before the backend hard-codes an enum — this spec keeps the enum as currently implemented, but flags it as unresolved.

### 2.2 Endpoints

| Method | Path | Auth | Status | Description |
|---|---|---|---|---|
| `GET` | `/users` | Admin | 🟢 | List staff accounts. Filters: `role`, `status` (`active`/`inactive` derived from `is_active`), `search` (name/email). Backs `pages/admin/users/index.vue`. |
| `POST` | `/users` | Admin | 🟢 | Create a staff account. Body per `AdminUserForm` fields: `first_name, last_name, email, tel, role, status, notes` (and optionally `password` — a random one is generated if omitted). `email` doubles as the login identifier and must be a valid address on the company domain (`@igeargeek.com`) — enforced server-side, not just a frontend hint. `must_change_password` is always set `true` on the created row — not a client-settable field — so every new account is forced through `POST /auth/change-password` on first use. |
| `GET` | `/users/:id` | Admin | 🟢 | Single staff record — `pages/admin/users/[id].vue`. |
| `PUT` | `/users/:id` | Admin | 🟢 | Full update. `email` is required and re-validated against the same `@igeargeek.com` rule as create. Supplying a non-empty `password` resets it and re-sets `must_change_password: true`, same as a fresh create. |
| `DELETE` | `/users/:id` | Admin | 🟢 | Soft-delete (`deleted_at` + `is_active: false`), not a hard delete — see §1.6. Recoverable via Trash/Restore below, same as Company/Contact/Deal/Lead. |
| `GET` | `/users/trash` | Admin | 🟢 | List soft-deleted accounts, paginated like `GET /users`. |
| `POST` | `/users/:id/restore` | Admin | 🟢 | Clears `deleted_at`/`deleted_by`. Does not re-set `is_active: true` — an Admin reactivates separately via `PUT /users/:id`, same as the account's other fields aren't re-derived on restore. |
| `GET` | `/team-members` | any authenticated | 🟢 | Lightweight `{ id, name, email }[]` list (`TeamMember` in `interfaces/crm.d.ts`) for assignee dropdowns (`CrmTeamMemberSelect`) — do not require Admin role for this one, every Sales role needs it to assign Leads/Deals/Tasks. |

---

## 3. Leads

`interfaces/crm.d.ts` → `Lead`:

```ts
type LeadStatus = 'New' | 'Contacted' | 'Qualified' | 'Disqualified'
type LeadSource = 'Referral' | 'Website' | 'Event' | 'Ads' | 'Other'

interface Lead {
  id: number
  name: string
  company_id: number | null   // Company.id — replaces the old free-text company_name (2026-08-24)
  email: string
  phone: string
  source: LeadSource
  status: LeadStatus
  notes: string
  assigned_to: number | null   // User.id
  tags: string[]
  converted_deal_id: number | null   // set once this Lead has been converted; prevents double-conversion
  score: number                       // FR-CRM-006 — sum of matching active LeadScoringCriterion weights
  classification: 'none' | 'mql' | 'sql'   // FR-CRM-007 — derived from score, or "sql" set manually by a rep
  prospect_id: number | null   // Prospect.id this Lead originated from, if created via POST /prospects/:id/convert (§3a) — null for a Lead created directly
  // Mirrors Deal.business_unit/business_unit_item (added 2026-09-03) — a
  // lightweight Project/Product tag, same nullable/enum-guarded shape.
  // Carried over to the Deal on conversion (frontend pre-fills the Deal
  // create form from it), same as channel/company_id/assigned_to already are.
  business_unit: 'Project' | 'Product' | null
  business_unit_item: string | null
  created_at: string
}
```

> `company_id` is nullable — a Lead can still exist with no Company picked yet, same as the old free-text `company_name` was optional. Existing rows were backfilled from their old `company_name` text at migration time (exact case-insensitive match against Companies, or a newly created Company when no match existed).

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/leads` | 🟢 | Filters: `status`, `source`, `assigned_to` (`unassigned` for `assigned_to IS NULL`), `company_id` (exact match), `exclude_converted=true`, `search` (name/email/**the joined Company's name**), `sort` (including `sort=company_name`, resolved via a join since it isn't a real column). Backs `pages/crm/leads/index.vue`. |
| `POST` | `/leads` | 🟢 | Create. `email`, if supplied, must be a syntactically valid address (not domain-restricted like staff `User.email` — a Lead's email belongs to an external contact) — `422` otherwise. Empty is fine; the field stays optional. `source` must be an active `LeadSourceOption` (§8.8). If `assigned_to` is omitted, the backend auto-assigns to whichever active Sales Rep currently has the fewest open Leads+Deals (round-robin by load). `classification` accepts only an explicit `"sql"` as a manual override — any other value defers to the auto-computed `score`/`classification` result. |
| `GET` | `/leads/:id` | 🟢 | Single lead. |
| `PUT` | `/leads/:id` | 🟢 | Update (including status transitions). Same `email`/`source` validation as Create. Omitting `classification` leaves an existing manual `"sql"` override in place rather than letting it fall back to the auto-computed value. |
| `DELETE` | `/leads/:id` | 🟢 | Soft-delete (§1.6) — recoverable via Trash/Restore below. |
| `GET` | `/leads/trash` | 🟢 | Sales Manager/Admin only. List soft-deleted leads, paginated like `GET /leads`. |
| `POST` | `/leads/:id/restore` | 🟢 | Sales Manager/Admin only. Clears `deleted_at`/`deleted_by`. |
| `PATCH` | `/leads/bulk-reassign` | 🟢 | Sales Manager/Admin only. Body: `{ ids: number[], assigned_to: number \| null }`. |
| `PATCH` | `/leads/bulk-tag` | 🟢 | Sales Manager/Admin only. Body: `{ ids: number[], tags: string[], mode: 'set' \| 'add' }` — `"set"` replaces each Lead's tags outright, `"add"` merges into the existing set. |
| `PATCH` | `/leads/bulk-archive` | 🟢 | Sales Manager/Admin only. Soft-deletes every listed Lead in one transaction (same effect as Delete, batched). |
| `POST` | `/leads/:id/convert` | 🟢 | Converts a Qualified Lead into a Deal (and Company/Contact if new) — `FR-CRM-004`. Body: `{ company_id?: number, contact_id?: number, deal: { title, value, stage, channel, ... } }`. Company resolution order: an explicit `company_id` in the request always wins; otherwise the Lead's own `company_id` is reused (falling back to creating a fresh Company only if that Company has since been soft-deleted); otherwise (a Lead with no Company at all) a new blank Company is created. `contact_id` omitted creates one from the Lead's `name`/`email`/`phone`. Any Attachments on the Lead are carried over to the new Deal. Response: `{ data: { deal: Deal, company: Company, contact: Contact } }`. |

---

## 3a. Prospects (`FR-CRM-105`/`106`)

Added 2026-09-01. The pre-Lead marketing funnel entity — Marketing works a Prospect (an early, often loosely-qualified Company/Contact lead) before it's ready to hand off to Sales via Convert, which mirrors §3's Lead→Deal `Convert` one funnel stage earlier: Prospect → Lead, instead of Lead → Deal. Everything about this section mirrors §3 (Leads) unless called out otherwise — same filters, same bulk-action shapes, same soft-delete/Trash/Restore semantics.

```ts
type ProspectStatus = 'New' | 'Engaging' | 'Nurturing' | 'Disqualified' | 'Converted'
type LeadSource = 'Referral' | 'Website' | 'Event' | 'Ads' | 'Other'   // same enum Lead.source uses — see §8.8

interface Prospect {
  id: number
  name: string
  company_id: number | null   // Company.id, same nullable-FK shape as Lead.company_id
  email: string
  phone: string
  source: LeadSource
  status: ProspectStatus
  notes: string
  assigned_to: number | null   // User.id — must be a Marketing/Sales Rep/Sales Manager/Admin account
  tags: string[]
  converted_lead_id: number | null   // set once this Prospect has been converted; prevents double-conversion
  // Mirrors Deal.business_unit/business_unit_item (added 2026-09-03). Carried
  // over to the new Lead automatically, server-side, on Convert — unlike
  // Lead→Deal's own Convert (which takes a Deal-style payload from the
  // frontend form and pre-fills client-side instead), this Convert builds
  // the whole Lead server-side, so the pass-through happens here directly,
  // same treatment `source` already gets.
  business_unit: 'Project' | 'Product' | null
  business_unit_item: string | null
  created_at: string
}
```

> `ProspectStatus` is a **fixed** enum (not Admin-configurable via an option list, unlike `PipelineStage`) — mirrors how `LeadStatus` is fixed too. `"Converted"` is set **only** by `POST /prospects/:id/convert` below — Create/Update reject a client-supplied `status: "Converted"` with `422` unless the Prospect is already Converted and the request is just re-submitting that same value unchanged (so a generic "edit this record" form that resends every field as-is isn't blocked).

| Method | Path | Auth | Status | Description |
|---|---|---|---|---|
| `GET` | `/prospects` | Admin, Marketing, Sales Manager | 🟢 | Filters: `status`, `source`, `assigned_to` (`unassigned` for `assigned_to IS NULL`), `company_id` (exact match), `exclude_converted=true`, `search` (name/email/**the joined Company's name**), `sort` (including `sort=company_name`). Same filter/sort/search shape as `GET /leads`. |
| `POST` | `/prospects` | Admin, Marketing, Sales Manager | 🟢 | Create. `email`, if supplied, must be a syntactically valid address (same `validateExternalEmail` rule as Lead — not domain-restricted, since it belongs to an external contact). `source` must be an active `LeadSourceOption` (§8.8, same option list Lead uses). `status` defaults to `"New"` when omitted; a client-supplied `"Converted"` is rejected (`422`) — see the note above. No auto-assignment on omitted `assigned_to` (unlike Lead's round-robin) — stays unassigned until a Marketing user/Admin/Sales Manager picks one. |
| `GET` | `/prospects/:id` | Admin, Marketing, Sales Manager | 🟢 | Single prospect. |
| `PUT` | `/prospects/:id` | Admin, Marketing, Sales Manager | 🟢 | Update (including status transitions other than into `"Converted"` — see the note above). Same `email`/`source` validation as Create. |
| `DELETE` | `/prospects/:id` | Admin, Marketing, Sales Manager | 🟢 | Soft-delete (§1.6) — recoverable via Trash/Restore below. |
| `GET` | `/prospects/trash` | Sales Manager/Admin only | 🟢 | List soft-deleted prospects, paginated like `GET /prospects`. Note: narrower than the read/write role set above — Marketing does not get Trash/Restore/bulk-*, same split Lead doesn't have (Lead's bulk-*/Trash/Restore are Sales-Manager/Admin-only too, but Lead's read/write set is every Sales role, not a 3-role allowlist). |
| `POST` | `/prospects/:id/restore` | Sales Manager/Admin only | 🟢 | Clears `deleted_at`/`deleted_by`. |
| `PATCH` | `/prospects/bulk-reassign` | Sales Manager/Admin only | 🟢 | Body: `{ ids: number[], assigned_to: number \| null }`. |
| `PATCH` | `/prospects/bulk-tag` | Sales Manager/Admin only | 🟢 | Body: `{ ids: number[], tags: string[], mode: 'set' \| 'add' }`. |
| `PATCH` | `/prospects/bulk-archive` | Sales Manager/Admin only | 🟢 | Soft-deletes every listed Prospect in one transaction. |
| `POST` | `/prospects/:id/convert` | Admin, Marketing, Sales Manager | 🟢 | Converts a Prospect into a Lead (and Company/Contact if new) — one funnel stage earlier than Lead's own Convert. Body: `{ company_id?: number, contact_id?: number, lead?: { assigned_to?: number } }`. Company resolution order: an explicit `company_id` in the request always wins; otherwise the Prospect's own `company_id` is reused (falling back to creating a fresh Company only if that Company has since been soft-deleted); otherwise (a Prospect with no Company at all) a new blank Company is created. `contact_id` omitted creates one from the Prospect's `name`/`email`/`phone`. The new Lead's `assigned_to` is `lead.assigned_to` if supplied, else falls back to the Prospect's own `assigned_to`. The new Lead's `prospect_id` back-references this Prospect (§3). Any Attachments on the Prospect are carried over to the new Lead. `409` if the Prospect has already been converted (`converted_lead_id` already set). Response: `{ data: { lead: Lead, company: Company, contact: Contact } }`. |

---

## 4. Companies

`interfaces/crm.d.ts` → `Company`:

```ts
type ActiveArchivedStatus = 'active' | 'archived'

interface Company {
  id: number
  name: string
  industry: string
  size: string
  revenue_size: string
  website: string
  tags: string[]        // Tag.name values
  notes: string
  status: ActiveArchivedStatus
  legal_name: string | null   // registered legal entity name — used on Contract PDF exports
  address: string | null      // registered address — used on Contract PDF exports
  tax_id: string | null       // used on Contract PDF exports
  created_at: string
  updated_at: string
}
```

> `industry`/`size`/`revenue_size` should be validated against the Admin-configurable option lists in §8.8 (`IndustryOption`/`CompanySizeOption`/`RevenueSizeOption`) rather than free text.

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/companies` | 🟢 | Filters: `status`, `tag`, `industry`, `search` (name). Backs `pages/crm/companies/index.vue`. |
| `POST` | `/companies` | 🟢 | Create. |
| `GET` | `/companies/:id` | 🟢 | Single company — `pages/crm/companies/[id].vue`'s Overview tab. |
| `PUT` | `/companies/:id` | 🟢 | Update. |
| `DELETE` | `/companies/:id` | 🟢 | Sets `status: 'archived'` (soft delete, §1.6) — never a hard delete, since Deals/Contacts/Payments reference `company_id`. |
| `POST` | `/companies/import` | 🟢 | Bulk import — see §6.2. `FR-CRM-014`. |

---

## 5. Contacts

`interfaces/crm.d.ts` → `Contact`:

```ts
interface Contact {
  id: number
  company_id: number
  name: string
  email: string
  phone: string
  role_title: string
  tags: string[]
  status: ActiveArchivedStatus
  created_at: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/contacts` | 🟢 | Filters: `company_id`, `status`, `tag`, `search` (name/email). Backs `pages/crm/contacts/index.vue` and the Company detail page's contact list. Also the source of truth for `pages/crm/deals/create.vue`'s "Primary Contact" field, which must only offer contacts belonging to the Deal's selected Company — done client-side today via `contactsStore.byCompany(company_id)` (a thin wrapper over this same `company_id` relationship) since the store already holds every contact from one unfiltered fetch; a backend serving this at scale should either keep that filter server-side per request or ensure the frontend switches to `?company_id=` here instead of fetching everything. |
| `POST` | `/contacts` | 🟢 | Create. |
| `GET` | `/contacts/:id` | 🟢 | Single contact — `pages/crm/contacts/[id].vue`. |
| `PUT` | `/contacts/:id` | 🟢 | Update. |
| `DELETE` | `/contacts/:id` | 🟢 | Soft-delete (`status: 'archived'`). |
| `POST` | `/contacts/import` | 🟢 | Bulk import — see §6.2, same FlowAccount-export path as Companies. |

> `FR-CRM-012` ("one Contact marked Primary per Company") is 🔜 **Planned** — no `is_primary` field exists in the frontend interface today. If added, it should live here as a boolean with a uniqueness constraint per `company_id`.

---

## 6. File uploads & bulk import

### 6.1 Conventions

- `multipart/form-data`, single field named `file`.
- Response for a single-file upload (Quote PDF, Contract signed doc): `{ data: { file_name: string, file_url: string, file_size: number, uploaded_at: string } }` — matches the optional fields already on `Quote` (`interfaces/crm.d.ts`).
- Max file size: 10 MB; returns `413` with the standard error envelope (§1.5) if exceeded.
- Allowed extensions: `.pdf .png .jpg .jpeg .doc .docx .xls .xlsx .csv` — anything else returns `400`. Deliberately excludes anything a browser would execute inline (`.html`, `.svg`, etc.), since uploaded files are served back from this API's own origin (see below).
- `file_url` currently resolves to this API's own `/uploads/<name>` (auth-gated — any authenticated role, same access level as the Quote/Contract PDF export endpoints; unauthenticated requests get `401`), backed by local disk. Store files in object storage (S3-compatible) instead before any real multi-replica deployment — local disk doesn't persist across redeploys or replicas. See [`s3-migration-plan.md`](s3-migration-plan.md) for the design; the `file_url` contract itself (a path this API serves, auth-gated the same way) is deliberately meant to stay unchanged by that migration — only what backs it changes.

### 6.2 Bulk import (Companies/Contacts)

Backs `components/Crm/ImportContactsModal.vue` — currently a **client-side-only** CSV/XLS/XLSX parser with no backend call; this endpoint replaces that parsing with a real server-side import so duplicate detection can be authoritative instead of best-effort in the browser.

| Method | Path | Status | Description |
|---|---|---|---|
| `POST` | `/companies/import` | 🟢 | Body: `file` (CSV/XLS/XLSX, FlowAccount export layout — see `ImportContactsModal.vue` for the exact column mapping it currently expects client-side). Response: `{ data: { created: number, updated: number, skipped: number, errors: { row: number, message: string }[] } }`. |
| `POST` | `/contacts/import` | 🟢 | Same shape, for the Contacts half of the same import file. |

> `FR-CRM-014` specifies dedup by email/domain; the current frontend parser dedupes by company **name** instead. Implement the backend against email/domain per the spec — this is a deliberate improvement over, not a mirror of, today's frontend behavior.

> Company dedup resolution order: normalized website domain (case-insensitive, scheme/`www.`/path stripped) first, falling back to a case-insensitive/whitespace-trimmed name match only when either side has no website. Backed by an indexed `domain` column maintained at write time (Create/Update/Import all populate it) rather than scanning every company with a website per imported row, so a large import stays an indexed lookup per row instead of an O(n) scan.

---

## 7. Deals, Activities, Tags, Quotes, Payments, Tasks

### 7.1 Deals

```ts
type DealStage = 'Lead' | 'Qualified' | 'Proposal Sent' | 'Negotiation' | 'Won' | 'Lost'
type DealStatus = 'open' | 'won' | 'lost'
type BusinessUnit = 'Project' | 'Product'

interface Deal {
  id: number
  company_id: number
  contact_id: number
  title: string
  value: number
  stage: DealStage
  status: DealStatus
  expected_close_date: string | null
  assigned_to: number | null
  channel: LeadSource
  business_unit: BusinessUnit | null
  business_unit_item: string | null   // free-text label, e.g. specific product/project name
  created_at: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/deals` | 🟢 | Filters: `stage`, `status`, `company_id`, `assigned_to`, `business_unit`, `channel`, `search` (title). `sort=company_name` resolved via a join (Deal has no such column). Backs both `pages/crm/deals/index.vue` (Kanban) and the dashboard's `filteredDeals`. |
| `POST` | `/deals` | 🟢 | Create. `value` must be ≥ 0; `expected_close_date`, if supplied, must parse as either a plain `YYYY-MM-DD` date or a full ISO 8601 timestamp (the two shapes the frontend actually sends) — `422` otherwise on either field. `stage`/`channel` must be an active `PipelineStage`/`LeadSourceOption` (§8.8); `probability`, if supplied, must be 0–100; `lost_reason` is required once `stage`/`status` moves to Lost. |
| `GET` | `/deals/:id` | 🟢 | Single deal — `pages/crm/deals/[id].vue` Overview tab. |
| `PUT` | `/deals/:id` | 🟢 | Full update. Same validation as Create. |
| `PATCH` | `/deals/:id/stage` | 🟢 | Body: `{ stage: DealStage }`. Dedicated endpoint for the Kanban drag-and-drop (`CrmPipelineBoard`'s `@move`) so the backend can also update `status` (open/won/lost) and fire `FR-CRM-064`'s auto Customer-Product creation (§8.2) in one transaction when stage becomes `Won`. |
| `DELETE` | `/deals/:id` | 🟢 | Soft-delete (§1.6) — recoverable via Trash/Restore below. |
| `GET` | `/deals/trash` | 🟢 | Sales Manager/Admin only. List soft-deleted deals, paginated like `GET /deals`. |
| `POST` | `/deals/:id/restore` | 🟢 | Sales Manager/Admin only. |
| `PATCH` | `/deals/:id/reassign` | 🟢 | Sales Manager/Admin only. Body: `{ assigned_to: number }`. |
| `PATCH` | `/deals/bulk-reassign` | 🟢 | Sales Manager/Admin only. Body: `{ ids: number[], assigned_to: number \| null }`. |
| `PATCH` | `/deals/bulk-tag` | 🟢 | Sales Manager/Admin only. Body: `{ ids: number[], tags: string[], mode: 'set' \| 'add' }`. |
| `PATCH` | `/deals/bulk-archive` | 🟢 | Sales Manager/Admin only. Soft-deletes every listed Deal in one transaction. |

### 7.2 Activities

```ts
type ActivityType = 'call' | 'email' | 'meeting'
type ActivityRelatedType = 'contact' | 'company' | 'deal' | 'prospect'   // 'prospect' added 2026-09-01 for §3a

interface Activity {
  id: number
  type: ActivityType
  subject: string
  notes: string
  related_type: ActivityRelatedType
  related_id: number
  created_by: string
  created_at: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/activities` | 🟢 | Filters: `related_type`, `related_id` (required together), `type`. Backs the timeline on Deal/Company/Contact detail pages. |
| `POST` | `/activities` | 🟢 | Create — `FR-CRM-031`'s manual entry form. |
| `DELETE` | `/activities/:id` | 🟢 | Delete. |

### 7.3 Tags

```ts
type TagCategory = 'Tier' | 'Industry' | 'Priority'
type TagStatus = 'active' | 'inactive'

interface Tag {
  id: number
  name: string
  category: TagCategory
  description: string
  status: TagStatus
  created_at: string
}
```

Tags are a shared taxonomy referenced by Company/Deal/Contact — writes are Admin/Sales-Manager only (same `bulkRoles` gate as Deal/Lead bulk actions) since renaming or deactivating a tag affects filtering/reporting for every user, not just its creator; `GET` stays open to every authenticated role since tag pickers on Company/Deal/Contact forms need it regardless of role.

| Method | Path | Auth | Status | Description |
|---|---|---|---|---|
| `GET` | `/tags` | any authenticated | 🟢 | Filters: `category`, `status`, `search` (name). Backs `pages/crm/tags/index.vue`. |
| `POST` | `/tags` | Admin, Sales Manager | 🟢 | Create. |
| `PUT` | `/tags/:id` | Admin, Sales Manager | 🟢 | Update. |
| `DELETE` | `/tags/:id` | Admin, Sales Manager | 🟢 | Soft-delete (`status: 'inactive'`). |

### 7.4 Quotes

```ts
type QuoteStatus = 'draft' | 'sent' | 'accepted' | 'rejected'
// 'expired' is a fifth, read-derived-only value: never settable directly via
// Create/Update, only ever returned by GET/List once a Sent quote's
// validity_date has passed (Quote.EffectiveStatus) — see quotes-expiring-soon
// (§8.4) for the forward-looking mirror of this same check.

type QuotePriceType = 'excl_tax' | 'incl_tax'   // display/PDF concern only — doesn't change how VAT is computed

interface QuoteItem {
  description: string
  qty: number
  price: number
  product_id?: number       // when set, the backend snapshots that Product's current name/price into
                             // description/price at save time — later Product edits don't retroactively
                             // change a saved quote
  discount_percent?: number // 0-100, reduces this item's own line total; independent of Quote.discount_total
}

interface Quote {
  id: number
  deal_id: number
  number: string | null          // generated, immutable document number (e.g. "QT2026080004"), assigned once at Create
  scope_of_work: string          // free-text engagement narrative, printed above the line-items table on export
  items: QuoteItem[]
  reference_number: string | null  // free-text, user-entered (e.g. the customer's own PO number)
  issue_date: string | null        // the quote's "as of" date
  validity_date: string | null
  credit_days: number
  price_type: QuotePriceType
  vat_enabled: boolean            // Thailand's fixed 7% VAT — no separate rate field, 7% is statutory
  wht_enabled: boolean            // withholding tax — varies by service type, so has its own rate
  wht_rate: number
  discount_total: number          // flat currency amount, subtracted once from the items' summed subtotal
  notes: string | null            // prints on the exported PDF (payment terms, validity terms, etc.)
  internal_notes: string | null   // never printed on the exported PDF
  status: QuoteStatus | 'expired'
  file_name?: string
  file_url?: string
  file_size?: number
  uploaded_at?: string
  extraction_status?: 'ok' | 'partial' | 'failed'   // see note below — only set on PDF-uploaded quotes
  extraction_warnings?: string[]
}
```

> **FlowAccount PDF extraction.** `POST /deals/:dealId/quotes/upload` attempts best-effort field extraction from an uploaded FlowAccount quotation PDF, pre-filling `number`/`scope_of_work`/`items`/dates/totals instead of leaving the Quote fully blank. `extraction_status`/`extraction_warnings` are `nil`/empty for every Quote created the normal line-item way (extraction never runs for those) — they're only set on the upload path:
> - `"ok"` — every field extraction looked for was found and self-consistent.
> - `"partial"` — some fields extracted; `extraction_warnings` lists what's missing or suspect (e.g. a recomputed total that doesn't match the PDF's printed one) — the rep should double-check those before Sending.
> - `"failed"` — the upload still succeeds and the file is still attached, but the PDF didn't look like a FlowAccount export at all (or had no readable text layer), so nothing was pre-filled.

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/deals/:dealId/quotes` | 🟢 | List quotes for a Deal. `status` on each row reflects `EffectiveStatus` (may report `expired`), not necessarily the raw stored value. |
| `POST` | `/deals/:dealId/quotes` | 🟢 | Create a line-item quote. `number` is always server-generated — not client-settable. |
| `POST` | `/deals/:dealId/quotes/upload` | 🟢 | Upload a PDF quote (§6.1) — sets `file_name/file_url/file_size/uploaded_at` and attempts FlowAccount field extraction (see above), setting `extraction_status`/`extraction_warnings` and pre-filling whatever fields it could read; `items` stays empty only if extraction found none. |
| `PUT` | `/quotes/:id` | 🟢 | Update status/items and every other field above (`number` excepted — immutable after Create). |
| `DELETE` | `/quotes/:id` | 🟢 | Delete. |
| `GET` | `/quotes/:id/export-pdf` | 🟢 | `FR-CRM-042` — returns a generated PDF (`github.com/go-pdf/fpdf`): document number, scope of work, line items table (with per-item discount and tax/WHT totals), Deal/Company/Contact header, validity date, status, and `notes` (never `internal_notes`). Read-only, same access level as List (no `CanWrite` ownership check). |

### 7.5 Payments

```ts
type PaymentMethod = 'cash' | 'transfer' | 'card' | 'other'

interface Payment {
  id: number
  deal_id: number
  amount: number
  paid_at: string
  method: PaymentMethod
  note: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/deals/:dealId/payments` | 🟢 | List installments for a Deal, plus a computed `total_paid`. Backs the Deal detail page's Payments tab (`stores/payments.ts`'s `forDeal`/`totalForDeal` getters — move that sum server-side once real). |
| `POST` | `/deals/:dealId/payments` | 🟢 | Create — backs `components/Crm/AddPaymentModal.vue`. |
| `DELETE` | `/payments/:id` | 🟢 | Delete. |

### 7.6 Tasks

```ts
type TaskStatus = 'pending' | 'done'
type TaskRelatedType = ActivityRelatedType   // 'contact' | 'company' | 'deal' | 'prospect'

interface Task {
  id: number
  related_type: TaskRelatedType
  related_id: number
  title: string
  due_date: string
  status: TaskStatus
  assigned_to: number | null
  created_at: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/tasks` | 🟢 | Filters: `related_type`+`related_id`, `status`, `assigned_to`. `status=pending` powers the dashboard's "Upcoming Follow-ups" widget across all related records — support that query without requiring `related_type`/`related_id`. |
| `POST` | `/tasks` | 🟢 | Create. |
| `PATCH` | `/tasks/:id/toggle` | 🟢 | Flips `pending`⇄`done` — mirrors `stores/tasks.ts`'s `toggleDone`. |
| `PATCH` | `/tasks/bulk-mark-done` | 🟢 | Body: `{ ids: number[] }`. `FR-CRM-032`'s bulk mark-done on the all-tasks page. Unlike Deal/Lead bulk endpoints (Admin/Sales-Manager only), this is open to every authenticated role — ownership is enforced per row (the same `assigned_to`-or-unassigned rule `PATCH /tasks/:id/toggle` uses), since a Sales Rep bulk-clearing their own backlog is the primary use case. A forbidden row rolls back the whole call — no partial apply. |
| `PATCH` | `/tasks/bulk-reassign` | 🟢 | Body: `{ ids: number[], assigned_to: number \| null }`. Same per-row ownership rule as above, checked against both the new assignee (once, up front) and each task's current assignee (per row) — a Sales Rep may bulk-reassign their own tasks to themselves/unassigned, not to a different rep. |
| `DELETE` | `/tasks/:id` | 🟢 | Delete. |
| — | *(reminder notifications)* | 🔜 | `FR-CRM-032`'s "notification on due" — no delivery mechanism (email/push) exists in the frontend; needs a scheduled job + `/6` integrations, out of scope for this v1 endpoint list. |

---

## 8. Planned entities

This section was originally written with nothing built on either side. That's no longer true for any of it: every subsection (§8.1 Contracts, §8.2 Products/Customer-Products, §8.3 Projects, §8.4 Reports, §8.5 Audit log, §8.6 Admin Settings, §8.7 Sales Targets, §8.8 Admin Configuration) now has real backend handlers **and** frontend pages/stores/interfaces consuming them — treat all of it as 🟢 **Required now**, kept under "Planned entities" as a section heading rather than moved up only to avoid re-plumbing every cross-reference into it.

### 8.1 Contracts (`FR-CRM-043`–`045`)

```ts
type ContractStatus = 'draft' | 'sent' | 'signed' | 'expired'

interface Contract {
  id: number
  deal_id: number
  quote_id: number | null   // the Quote a Contract's PDF pulls line items/total from — optional,
                              // a Contract can be drafted before a Quote is finalized
  status: ContractStatus
  signed_file_url: string | null
  signed_date: string | null
  created_at: string
}
```

| Method | Path | Description |
|---|---|---|
| `GET` | `/deals/:dealId/contracts` | List. |
| `POST` | `/deals/:dealId/contracts` | Create. |
| `PUT` | `/contracts/:id` | Update status/`quote_id`. |
| `POST` | `/contracts/:id/upload` | Upload the signed document (§6.1) → sets `signed_file_url`/`signed_date`. |
| `GET` | `/contracts/:id/export-pdf` | Returns a generated PDF pulling line items/total from the linked Quote (if any), Deal/Company header, and Company `legal_name`/`address`/`tax_id` (§4) as the registered party details. |

### 8.2 Product Catalog & Customer-Product tracking (`FR-CRM-060`–`066`)

```ts
interface Product {
  id: number
  name: string
  category: string
  description: string
  is_active: boolean
}

type CustomerProductStatus = 'Interested' | 'Trial' | 'Active' | 'Churned'

interface CustomerProduct {
  id: number
  company_id: number
  product_id: number
  status: CustomerProductStatus
  start_date: string
  end_date: string | null
  source_deal_id: number | null
}
```

| Method | Path | Description |
|---|---|---|
| `GET` / `POST` | `/products` | Product Catalog CRUD (Admin only). |
| `PATCH` | `/products/:id/deactivate` | Sets `is_active: false` rather than deleting. |
| `GET` | `/companies/:companyId/products` | List a Company's Customer-Product records — powers the Company profile's "Products in use" section (`FR-CRM-066`). |
| `POST` | `/companies/:companyId/products` | Manually add/change status independent of a Deal (`FR-CRM-065`). |
| `PATCH` | `/customer-products/:id` | Update a Customer-Product's own `status`/`end_date` after creation (e.g. Interested → Trial → Active → Churned) — `company_id`/`product_id` are immutable. Writes a `customer_product`/`status_changed` audit entry (§8.5) when `status` actually changes. |
| **Side effect, not a separate endpoint** | — | When `PATCH /deals/:id/stage` (§7.1) sets `stage: 'Won'`, the backend must auto-create/update a `CustomerProduct` (`status: 'Active'`) for each Product on that Deal's accepted Quote (`FR-CRM-064`). Implement inside that same transaction, not as a client-triggered follow-up call. Still not implemented — deferred until Quotes have a real "accepted" flow. |

### 8.3 Projects (`FR-CRM-067`–`071`)

```ts
type ProjectStatus = 'Not Started' | 'In Progress' | 'On Hold' | 'Completed' | 'Cancelled'

interface Project {
  id: number
  company_id: number
  deal_id: number | null
  name: string
  status: ProjectStatus
  start_date: string
  target_end_date: string | null
  production_reference: string | null   // free-text ID and/or URL into Production's own system
  notes: string
  company_name?: string   // only present on GET /projects rows (see below) — the per-company list doesn't merge this in
}
```

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/companies/:companyId/projects` | any | List — Company profile's "Projects" section (`FR-CRM-070`). |
| `POST` | `/companies/:companyId/projects` | Sales/Admin | Create manually, or prompted when a Deal is marked Won (`FR-CRM-068`). |
| `GET` | `/projects` | any | Cross-company list — a global Projects view, since `/companies/:companyId/projects` can only show one company at a time. Supports `status`, `company_id` filters; each row's `company_name` is merged in the same way `/companies/:companyId/products` merges Product into CustomerProduct. |
| `PATCH` | `/projects/:id` | Sales/Admin **or** Production (§1.7) | Production's role is scoped to `status` and `production_reference` only — enforce field-level, not just endpoint-level, authorization here: reject the request if the body contains any other key, don't just silently drop them. Writes a `project`/`status_changed` audit entry (§8.5) when `status` actually changes, same as `PATCH /customer-products/:id`. `components/Crm/AddProjectModal.vue` mirrors this client-side — a Production caller only ever sees/submits `status`/`production_reference`, since submitting the full field set would 403 against this same restriction. |

Do **not** add sub-resources for tasks/sprints/milestones under `/projects/:id` — `FR-CRM-071` explicitly rules this out; a Project here is a summary record, never a delivery-management tool.

### 8.4 Reports (`FR-CRM-054`, `056`, `093`–`098`)

All `/reports/*` endpoints are Sales Manager/Admin only (route-gated, same as §1.7). Every list-shaped report returns `[]` on an empty result, never `null` — a nil Go slice marshals to `null` and the frontend's `.map()`/`.length` on the response body would throw. Every report below is pre-sorted so the most urgent/highest-value row leads (see each row's description for the exact key) — the frontend shouldn't need to re-sort client-side.

Every report also has a `GET /reports/<path>/export` counterpart taking the exact same query params and returning the same rows as `text/csv` (same `Content-Disposition: attachment` convention as §6's other CSV exports) — e.g. `GET /reports/stalled-deals/export?assigned_to=12`. Not repeated as separate rows below; assume every path in this section has one.

Two report shapes the frontend dashboard doesn't compute today because the underlying data (lead-source conversion, product/project status) doesn't exist yet:

| Method | Path | Description |
|---|---|---|
| `GET` | `/reports/lead-source-conversion?assigned_to=&date_from=&date_to=` | Conversion rate by `Lead.source` (`FR-CRM-054`) — `[{ source, total, qualified, conversion_rate }]`, sorted by `total` descending. |
| `GET` | `/reports/customers-by-product-status?product_id=&status=&company_tag=` | "Which customers use Product X" / "have a Project in status Y" (`FR-CRM-056`) — do not confuse with the `business_unit`/`channel` filters in §9, which are lightweight Deal tags, not this real relationship query. Sorted by `start_date` descending (most recently onboarded first). |

Six more, going beyond the dashboard's aggregate stat cards into "which specific records need attention." `company_tag`, where accepted, matches the same tag-array-overlap semantics as every other `company_tag` filter in this spec (§9):

| Method | Path | Description |
|---|---|---|
| `GET` | `/reports/win-loss-reasons?date_from=&date_to=&assigned_to=&company_tag=` | `FR-CRM-093`. Every closed Deal (`won` or `lost`), grouped by `"won"` or its `lost_reason` code — `[{ reason, count, value }]`, sorted by `count` descending. Answers "why are we losing," not just the dashboard's win-rate number. A lost Deal missing `lost_reason` (shouldn't happen given `lost_reason`'s required-on-Lost validation, but tolerated defensively) groups under `"other"` rather than being dropped. |
| `GET` | `/reports/stalled-deals?min_days=&assigned_to=&company_tag=` | `FR-CRM-094`. Open Deals with no logged Activity for at least `min_days` (default 14, falling back to the Deal's own `created_at` if it has never had one) — `[{ deal_id, title, company_name, stage, value, assigned_to, last_activity_at, days_stalled }]`, sorted by `days_stalled` descending (coldest first). Surfaces deals quietly going cold, not yet marked Lost. |
| `GET` | `/reports/outstanding-balance?assigned_to=&company_tag=` | `FR-CRM-095`. Won Deals whose recorded Payments sum to less than the Deal's `value` — `[{ deal_id, deal_title, company_name, deal_value, paid_amount, outstanding_amount }]`, sorted by `outstanding_amount` descending, every row money still owed. A flat list, not 30/60/90-day aging — `Payment` has no due-date field, only `paid_at` (when actually received), so aging-by-due-date isn't possible until that field exists. |
| `GET` | `/reports/quotes-expiring-soon?within_days=&assigned_to=&company_tag=` | `FR-CRM-096`. Sent quotes (not yet Accepted/Rejected) whose `validity_date` falls within the next `within_days` (default 7) — `[{ quote_id, deal_id, deal_title, company_name, validity_date, total_value }]`, sorted by `validity_date` ascending (soonest-to-expire first). The forward-looking mirror of `Quote`'s `EffectiveStatus`-derived `expired` state (§7.4) — same permissive RFC3339-or-bare-date `validity_date` parsing, a value that fails to parse is silently skipped rather than erroring the whole report. `assigned_to`/`company_tag` match against each quote's parent Deal (there's no single SQL join spanning quotes/deals/companies here, so this is resolved in application code). |
| `GET` | `/reports/contracts-stuck?min_days=&assigned_to=&company_tag=` | `FR-CRM-097`. Contracts sitting in `draft` or `sent` for at least `min_days` (default 14) without being signed — `[{ contract_id, deal_id, deal_title, company_name, status, assigned_to, days_in_status }]`, sorted by `days_in_status` descending (longest-stalled first). `assigned_to` here is the parent Deal's assignee, not a field on `Contract` itself. `Contract` has no start/end date to measure true expiration by (only `signed_date`, set once actually signed), so this tracks staleness before signature instead — the contract-side equivalent of `stalled-deals` above. |
| `GET` | `/reports/projects-at-risk?company_tag=` | `FR-CRM-098`. Projects whose `target_end_date` has already passed but whose `status` isn't `Completed`/`Cancelled` — `[{ project_id, name, company_id, company_name, status, target_end_date, days_overdue }]`, sorted by `days_overdue` descending. The delivery-side equivalent of `stalled-deals`, for whoever owns customer-delivery visibility (§8.3). No `assigned_to` filter — `Project` has no owner/assignee field, only a Company FK. |

### 8.5 Audit log (`FR-CRM-082`)

```ts
interface AuditLogEntry {
  id: number
  entity_type: string       // 'deal' | 'project' | 'customer_product' | ...
  entity_id: number
  action: string             // e.g. 'stage_changed', 'status_changed'
  before: Record<string, unknown> | null
  after: Record<string, unknown> | null
  actor_id: number
  created_at: string
}
```

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/audit-log` | Admin | Filters: `entity_type`, `entity_id`, `actor_id`, date range. Must be **append-only** at the storage layer (`NFR-007`) — no `PUT`/`DELETE` route should exist for this resource at all. |

At minimum, write an entry whenever: a Deal's `stage` changes, a Deal's `status` becomes `won`/`lost`, or a `CustomerProduct`/`Project` `status` changes (per `FR-CRM-082`'s explicit minimum scope) — all four are now implemented (`deals.go` for the Deal events, `projects.go`/`products.go` for the other two). The frontend's `/admin/activity-log` page is already repointed at this real endpoint.

### 8.6 Admin Settings (`FR-CRM-058`, `FR-CRM-091`)

A singleton row (`AppSettings`, always `id: 1`) holding two Admin-configurable, company-wide figures that don't warrant their own table. Seeded on first run (`quarterly_sales_target: 3000000`, `annual_revenue_goal: 12000000`) so `GET /dashboard/summary` always has a value even before an Admin has ever touched this screen.

```ts
interface AppSettings {
  id: number
  quarterly_sales_target: number   // FR-CRM-058 — feeds §9's pipeline_coverage_ratio
  annual_revenue_goal: number      // FR-CRM-091 — feeds §9's annual_revenue_progress_ratio/annual_revenue_trend
  updated_at: string               // "last updated" hint for the Admin config UI — neither figure resets itself automatically
}
```

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/admin/settings` | Admin | Returns the singleton row. |
| `PATCH` | `/admin/settings` | Admin | Body: `{ quarterly_sales_target: number, annual_revenue_goal: number }`. **Both fields are required on every request** — this is a singleton config row, not a per-field partial-update resource, so an omitted field is a `422` rather than "leave it unchanged." Both must be `>= 0`. Writes an audit-log entry (`entity_type: "settings"`, `action: "updated"`) only when at least one value actually changed — a no-op PATCH (identical values) doesn't add a second entry. |

`annual_revenue_goal` (`FR-CRM-091`) has no automatic per-year reset — an Admin is expected to update it manually at the start of each calendar year (`updated_at` above is the intended staleness signal for that). `annual_revenue_trend` in §9 always tracks the **current** calendar year (Jan through the current month) regardless of when the goal was last set.

### 8.7 Per-quarter Sales Targets (`FR-CRM-092`)

`quarterly_sales_target` (§8.6) is one flat annual figure, always divided by 4 for "whatever quarter it is right now" — fine until an Admin wants to set a *different* target for a specific quarter (e.g. a higher Q4 push, or pre-setting next year's Q1 before this year ends). `SalesTarget` is a row-per-`(year, quarter)` table that overrides the flat fallback for its own period only — purely additive: a quarter with no row just falls back to `quarterly_sales_target / 4`, so nothing changes for an Admin who never touches this.

```ts
interface SalesTarget {
  id: number
  year: number
  quarter: number       // 1-4
  target_value: number  // the true quarterly figure — NOT divided by 4 the way quarterly_sales_target is
  created_at: string
  updated_at: string
  created_by: number | null
  updated_by: number | null
}
```

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/admin/sales-targets` | Admin | Filter: `year`. Returns every row (optionally scoped to one year), ordered oldest-to-newest. |
| `POST` | `/admin/sales-targets` | Admin | Body: `{ year: number, quarter: number, target_value: number }`. `quarter` must be 1–4, `year` a plausible calendar year, `target_value >= 0` and required. One row per `(year, quarter)` — a second `POST` for an already-targeted period is a `422` pointing the caller at `PATCH` instead. |
| `PATCH` | `/admin/sales-targets/:id` | Admin | Same body/validation as `POST`. Moving a row's `(year, quarter)` onto another existing row's period is rejected the same way. |
| `DELETE` | `/admin/sales-targets/:id` | Admin | Reverts that period back to the flat `quarterly_sales_target / 4` fallback — safe and reversible (re-`POST` the same period to restore the override). |

Every write here also invalidates `GET /dashboard/summary`'s response cache (§9) — a `SalesTarget` change affects `pipeline_coverage_ratio`/`quarterly_sales_target` without ever touching the `deals` table, so nothing else would surface the change promptly otherwise.

### 8.8 Admin Configuration (option lists, lead scoring, notifications)

🟢 **Required now** — fully implemented and routed (Admin only, `adminOnly` middleware) but previously undocumented here. Replaces several enums/lists that used to be hardcoded (`DealStage`, `LeadSource`) or unconstrained free text (`Company.industry/size/revenue_size`, `Contact.role_title`, Product category) with Admin-editable, DB-backed option rows — every Create/Update endpoint elsewhere in this spec that references one of these values (Lead/Deal `source`/`channel`, Deal `stage`, Contact `role_title`, etc.) validates against the corresponding active row here rather than a fixed Go enum.

Every option-list resource below (`PipelineStage`, `LeadSourceOption`, `IndustryOption`, `CompanySizeOption`, `RevenueSizeOption`, `JobTitleOption`, `ProductCategoryOption`) shares the same shape and endpoint pattern:

```ts
interface PipelineStage {          // the only one with extra fields — see note below
  id: number
  name: string                     // unique
  sort_order: number
  is_active: boolean
  is_won_stage: boolean            // this stage's Deals auto-transition status to "won"
  is_lost_stage: boolean           // this stage's Deals auto-transition status to "lost", require lost_reason
  created_at: string
}

interface OptionRow {              // LeadSourceOption / IndustryOption / CompanySizeOption /
  id: number                       // RevenueSizeOption / JobTitleOption / ProductCategoryOption
  name: string                     // unique
  is_active: boolean
  created_at: string
}
```

| Method | Path | Description |
|---|---|---|
| `GET` / `POST` | `/admin/pipeline-stages` | List / create a `PipelineStage`. Seeded on first run from the previously hardcoded `DealStage` enum (`Lead, Qualified, Proposal Sent, Negotiation, Won, Lost`) so existing Deals validate unchanged. |
| `PATCH` / `DELETE` | `/admin/pipeline-stages/:id` | Update (including `is_active`/`sort_order`/`is_won_stage`/`is_lost_stage`) / delete. |
| `GET` / `POST` | `/admin/lead-sources` | List / create a `LeadSourceOption` — shared by `Lead.source` and `Deal.channel`. Seeded from the retired `LeadSource` enum (`Referral, Website, Event, Ads, Other`). |
| `PATCH` / `DELETE` | `/admin/lead-sources/:id` | Update / delete. |
| `GET` / `POST` | `/admin/industries` | List / create an `IndustryOption` for `Company.industry`. Seeded from the frontend's retired `INDUSTRY_OPTIONS` constant. |
| `PATCH` / `DELETE` | `/admin/industries/:id` | Update / delete. |
| `GET` / `POST` | `/admin/company-sizes` | List / create a `CompanySizeOption` for `Company.size`. No prior hardcoded list — the seeded rows (`1-10` … `1000+`) are a tunable starting point, not a fixed business rule. |
| `PATCH` / `DELETE` | `/admin/company-sizes/:id` | Update / delete. |
| `GET` / `POST` | `/admin/revenue-sizes` | List / create a `RevenueSizeOption` for `Company.revenue_size`. Same "tunable starting point" framing as company-sizes. |
| `PATCH` / `DELETE` | `/admin/revenue-sizes/:id` | Update / delete. |
| `GET` / `POST` | `/admin/job-titles` | List / create a `JobTitleOption` for `Contact.role_title`. |
| `PATCH` / `DELETE` | `/admin/job-titles/:id` | Update / delete. |
| `GET` / `POST` | `/admin/product-categories` | List / create a `ProductCategoryOption` for `Product.category`. |
| `PATCH` / `DELETE` | `/admin/product-categories/:id` | Update / delete. |

**Lead scoring criteria** (`FR-CRM-006`) — weighted rules summed into `Lead.score`:

```ts
interface LeadScoringCriterion {
  id: number
  name: string
  field: string          // e.g. "source", "has_company_name", "has_phone" — matched against the scored Lead
  match_value: string    // compared against `field`'s value on the Lead (e.g. a specific LeadSource name)
  weight: number          // added to Lead.score when this criterion matches
  is_active: boolean
  created_at: string
}
```

| Method | Path | Description |
|---|---|---|
| `GET` / `POST` | `/admin/lead-scoring-criteria` | List / create. |
| `PATCH` / `DELETE` | `/admin/lead-scoring-criteria/:id` | Update / delete. |

**Workflow notification rules** (`FR-CRM-100`–`102`, `prospect` added `FR-CRM-107`) — Admin-configurable thresholds for the in-app "this needs attention" notifications (a Deal idle in-stage, a Quote about to expire, a Contract stuck unsigned, a Prospect gone stale):

```ts
type NotificationEntityType = 'deal' | 'quote' | 'contract' | 'prospect'
type NotificationRecipientRole = 'owner' | 'owner_and_managers'

interface NotificationRule {
  id: number
  name: string
  entity_type: NotificationEntityType
  threshold_days: number   // "deal": days idle in its current stage. "quote": days until validity_date.
                             // "contract": days sitting Draft/Sent without being signed.
                             // "prospect": days since updated_at with no change, while status is
                             // not yet Converted/Disqualified (added 2026-09-03, FR-CRM-107).
  recipient_role: NotificationRecipientRole
  is_active: boolean
  created_at: string
}

interface NotificationLogEntry {
  id: number
  rule_id: number
  entity_id: number
  context: string          // the Deal's stage at the time of firing (for "deal" rules); "" otherwise
  notified_at: string
}
```

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` / `POST` | `/admin/notification-rules` | Admin | List / create a `NotificationRule`. |
| `PATCH` / `DELETE` | `/admin/notification-rules/:id` | Admin | Update / delete. |
| `GET` | `/notification-log` | any authenticated | Recent rule firings for the caller's own entities (per-row ownership scoping happens inside the handler, not a role gate) — powers an in-app notification feed. |

---

## 9. Dashboard / reporting aggregates

`pages/index.vue` (Sales Pipeline Dashboard) currently computes every stat client-side from the full `deals`/`companies`/`contacts`/`tasks` store contents. That doesn't scale past mock-data volumes (`NFR-003` targets 2s page loads up to ~10,000 records) — replace the client-side computation with one aggregate endpoint instead of shipping full record sets to the browser.

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/dashboard/summary` | 🟢 | Query params mirror the dashboard's filter bar exactly: `date_from`, `date_to` (or a `period` preset: `all\|month\|quarter\|year\|last6\|last12`), `business_unit`, `business_unit_item`, `channel`. Returns every stat card + chart the page renders in one response (shape below). Responses are cached server-side per exact query-param combination for up to 30s, so a write immediately followed by a refresh may briefly still show pre-write numbers — acceptable staleness for a stat-card dashboard, but worth knowing if a future test asserts read-after-write freshness here specifically. |

Response shape (one object covering every widget on `pages/index.vue`):

```json
{
  "data": {
    "open_pipeline_value": 4820000,
    "won_value": 1250000,
    "win_rate": 42,
    "open_deals_count": 18,
    "forecasted_revenue": 2150000,
    "avg_deal_size": 185000,
    "avg_sales_cycle_days": 34,
    "pipeline_coverage_ratio": 1.6,
    "quarterly_sales_target": 3000000,
    "annual_revenue_goal": 12000000,
    "annual_revenue_actual": 5230000,
    "annual_revenue_progress_ratio": 0.436,
    "annual_revenue_trend": [ { "label": "Jan", "actual": 820000, "goal_pace": 1000000 }, "...Jan through the current month, cumulative" ],
    "revenue_trend": [ { "label": "Mar", "value": 320000 }, "...trailing 6 months, Won revenue by close month" ],
    "forecast_trend": [ { "label": "Mar", "value": 410000 }, "...next 6 months, open-deal value × probability by expected_close_date month" ],
    "stage_breakdown": [ { "stage": "Qualified", "value": 900000, "count": 4 }, "...per DealStage" ],
    "industry_breakdown": [ { "industry": "Retail", "win_rate": 55, "won_count": 6 }, "..." ],
    "team_performance": [ { "user_id": 3, "name": "...", "won_count": 5, "won_value": 620000, "win_rate": 60 }, "..." ],
    "upsell_opportunities": [ /* stale-contact candidates grouped by tier, see FR in dashboard hint copy */ ]
  }
}
```

`quarterly_sales_target` (`FR-CRM-058`) and `annual_revenue_goal`/`annual_revenue_actual`/`annual_revenue_progress_ratio`/`annual_revenue_trend` (`FR-CRM-091`) are Admin-configurable via `PATCH /admin/settings` — see §8.6. `quarterly_sales_target`'s value resolves through §8.7's per-quarter override first (a `SalesTarget` row for the current `(year, quarter)`, if an Admin has set one) before falling back to `quarterly_sales_target(annual) / 4` — this response field always reports whichever one actually applies to the current quarter, not the raw annual figure. `annual_revenue_trend`'s `actual` is a running cumulative total through each month (not that month's own delta); `goal_pace` is a straight-line `annual_revenue_goal × months_elapsed / 12` for the same point, letting the frontend chart whether the company is ahead of or behind pace, not just infer it from today's single ratio. Both `revenue_trend`/`forecast_trend` and `annual_revenue_trend` deliberately ignore this endpoint's own `business_unit`/`channel`/`date_from`/`date_to` filters (fixed trailing-6-months and fixed calendar-year views respectively, not filtered slices) — only the top-level stat cards and `stage_breakdown`/`industry_breakdown`/`team_performance` respect them.

---

## 10. Cross-cutting non-functional requirements

Carried over from `feature-spec.md` §5 where they bear directly on the API:

| ID | Requirement |
|---|---|
| NFR-001 | RBAC (§1.7) enforced server-side on every route — never rely on the frontend hiding a button. |
| NFR-003 | List/dashboard endpoints must respond within budget for ~10,000 Company/Contact/Deal rows — this is the reason §9 is one aggregate endpoint instead of the frontend fetching full tables. |
| NFR-004 | HTTPS/TLS only; passwords hashed (bcrypt/argon2) — never returned in any response, including the `User` shape in §2.1. |
| NFR-007 | Audit log (§8.5) is append-only — no update/delete route. |

---

## 11. Build order recommendation

1. **Auth + Users** (§2) — everything else needs a bearer token to test.
2. **Companies + Contacts** (§4–5), including import (§6.2) — every other entity hangs off these (per `feature-spec.md` §8's own build-order note).
3. **Leads + Deals** (§3, §7.1), including the `/deals/:id/stage` Kanban endpoint.
4. **Activities + Tasks + Tags + Payments** (§7.2, §7.3, §7.5, §7.6) — needed for the Deal/Company detail pages to be feature-complete against what's already built in the frontend.
5. **Dashboard aggregate** (§9) — once Deals/Companies exist with real volume, replace the frontend's client-side computation.
6. **Everything in §8** (Quotes, Contracts, Products/Projects, Audit log) — build alongside the corresponding frontend work, since none of it exists on either side yet. Start with Product Catalog + Customer-Product (§8.2), since `feature-spec.md` calls it out as the highest-value gap.

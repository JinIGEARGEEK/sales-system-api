# API System Specification — I GEAR GEEK Sales CRM

**Companion document to:** `feature-spec.md` (business requirements), `user-story.md` (role acceptance criteria), `design-system.md` (frontend conventions)
**Purpose:** The contract for the backend API this frontend is built against. This frontend (`sales-system`) is currently **100% client-side mock data** (Pinia stores seeded from `constants/mockData/`, see `design-system.md` §8) — no real HTTP calls exist yet beyond the `axios` plugin scaffold (`plugins/axios.ts`) and the `useMutateApi`/`useFetchApi` composables (`composables/utils/useAPI.ts`). This document specifies the API a **separate backend repo/project** must implement so this frontend can be wired up for real, resource by resource, without changing its existing conventions.
**Audience:** Backend engineering team / AI coding agent implementing the API in another repository.
**Version:** 1.1 (adds Thai role/use-case summary)
**Date:** 2026-08-14

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
| `POST` | `/auth/logout` | Bearer | 🟢 | Invalidates the token server-side if using a blocklist; frontend clears `localStorage` regardless (`useAuth().removeAccessToken()`). |
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
  company_name: string
  email: string
  phone: string
  source: LeadSource
  status: LeadStatus
  notes: string
  assigned_to: number | null   // User.id
  created_at: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/leads` | 🟢 | Filters: `status`, `source`, `assigned_to`, `search` (name/company_name/email). Backs `pages/crm/leads/index.vue`. |
| `POST` | `/leads` | 🟢 | Create. `email`, if supplied, must be a syntactically valid address (not domain-restricted like staff `User.email` — a Lead's email belongs to an external contact) — `422` otherwise. Empty is fine; the field stays optional. |
| `GET` | `/leads/:id` | 🟢 | Single lead. |
| `PUT` | `/leads/:id` | 🟢 | Update (including status transitions). Same `email` validation as Create. |
| `DELETE` | `/leads/:id` | 🟢 | Delete. |
| `POST` | `/leads/:id/convert` | 🟢 | Converts a Qualified Lead into a Deal (and Company/Contact if new) — `FR-CRM-004`. Body: `{ company_id?: number, contact_id?: number, deal: { title, value, stage, ... } }` — if `company_id`/`contact_id` omitted, backend creates them from the Lead's `company_name`/`email`/`phone`. Response: `{ data: { deal: Deal, company: Company, contact: Contact } }`. |

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
  website: string
  tags: string[]        // Tag.name values
  notes: string
  status: ActiveArchivedStatus
  created_at: string
  updated_at: string
}
```

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
- `file_url` currently resolves to this API's own `/uploads/<name>` (auth-gated — any authenticated role, same access level as the Quote/Contract PDF export endpoints; unauthenticated requests get `401`), backed by local disk. Store files in object storage (S3-compatible) instead before any real multi-replica deployment — local disk doesn't persist across redeploys or replicas.

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
| `GET` | `/deals` | 🟢 | Filters: `stage`, `status`, `company_id`, `assigned_to`, `business_unit`, `channel`, `search` (title). Backs both `pages/crm/deals/index.vue` (Kanban) and the dashboard's `filteredDeals`. |
| `POST` | `/deals` | 🟢 | Create. `value` must be ≥ 0; `expected_close_date`, if supplied, must parse as either a plain `YYYY-MM-DD` date or a full ISO 8601 timestamp (the two shapes the frontend actually sends) — `422` otherwise on either field. |
| `GET` | `/deals/:id` | 🟢 | Single deal — `pages/crm/deals/[id].vue` Overview tab. |
| `PUT` | `/deals/:id` | 🟢 | Full update. Same `value`/`expected_close_date` validation as Create. |
| `PATCH` | `/deals/:id/stage` | 🟢 | Body: `{ stage: DealStage }`. Dedicated endpoint for the Kanban drag-and-drop (`CrmPipelineBoard`'s `@move`) so the backend can also update `status` (open/won/lost) and fire `FR-CRM-064`'s auto Customer-Product creation (§8.2) in one transaction when stage becomes `Won`. |
| `DELETE` | `/deals/:id` | 🟢 | Delete. |
| `PATCH` | `/deals/:id/reassign` | 🔜 | Body: `{ assigned_to: number }`. Separate from the general `PUT` so the backend can append to an owner-history log — `FR-CRM-025`/`M-8`, not built in the frontend yet. |

### 7.2 Activities

```ts
type ActivityType = 'call' | 'email' | 'meeting'
type ActivityRelatedType = 'contact' | 'company' | 'deal'

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

interface QuoteItem {
  description: string
  qty: number
  price: number
}

interface Quote {
  id: number
  deal_id: number
  items: QuoteItem[]
  validity_date: string | null
  status: QuoteStatus
  file_name?: string
  file_url?: string
  file_size?: number
  uploaded_at?: string
}
```

| Method | Path | Status | Description |
|---|---|---|---|
| `GET` | `/deals/:dealId/quotes` | 🔜 | List quotes for a Deal. **Planned** — `FR-CRM-040`/`041`; today Quotes are a mock array embedded directly in the Deal detail page, no dedicated CRUD exists in the frontend either. |
| `POST` | `/deals/:dealId/quotes` | 🔜 | Create a line-item quote. |
| `POST` | `/deals/:dealId/quotes/upload` | 🔜 | Upload a PDF quote in place of line items (§6.1) — sets `file_name/file_url/file_size/uploaded_at`, leaves `items` empty. |
| `PUT` | `/quotes/:id` | 🔜 | Update status/items/validity_date. |
| `DELETE` | `/quotes/:id` | 🔜 | Delete. |
| `GET` | `/quotes/:id/export-pdf` | 🟢 | `FR-CRM-042` — returns a generated PDF (`github.com/go-pdf/fpdf`): line items table, Deal/Company/Contact header, validity date, status. Read-only, same access level as List (no `CanWrite` ownership check). |

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
type TaskRelatedType = ActivityRelatedType   // 'contact' | 'company' | 'deal'

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
| `DELETE` | `/tasks/:id` | 🟢 | Delete. |
| — | *(reminder notifications)* | 🔜 | `FR-CRM-032`'s "notification on due" — no delivery mechanism (email/push) exists in the frontend; needs a scheduled job + `/6` integrations, out of scope for this v1 endpoint list. |

---

## 8. Planned entities

This section was originally written with nothing built on either side. That's no longer true for every subsection: §8.2 (Products/Customer-Products), §8.3 (Projects), and §8.5 (Audit log) now have real backend handlers **and** frontend pages/stores/interfaces consuming them — treat those as 🟢 **Required now**, kept here rather than moved up only to avoid re-plumbing every cross-reference into them. §8.1 (Contracts) and §8.4 (Reports) remain 🔜 **Planned** in full — no page, store, or interface for either exists in the frontend yet. They're specified here so the backend can be built ahead of or alongside the frontend work, per `feature-spec.md` §3.5/§3.7/§3.8. Do not treat their absence from the frontend today as "not needed" — `feature-spec.md` calls §3.7 (Products/Projects) "the core addition" to this CRM.

### 8.1 Contracts (`FR-CRM-043`–`045`)

```ts
type ContractStatus = 'draft' | 'sent' | 'signed' | 'expired'

interface Contract {
  id: number
  deal_id: number
  status: ContractStatus
  signed_file_url: string | null
  signed_date: string | null
}
```

| Method | Path | Description |
|---|---|---|
| `GET` | `/deals/:dealId/contracts` | List. |
| `POST` | `/deals/:dealId/contracts` | Create. |
| `PUT` | `/contracts/:id` | Update status. |
| `POST` | `/contracts/:id/upload` | Upload the signed document (§6.1) → sets `signed_file_url`/`signed_date`. |

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

### 8.4 Reports (`FR-CRM-054`, `056`)

Two report shapes the frontend dashboard doesn't compute today because the underlying data (lead-source conversion, product/project status) doesn't exist yet:

| Method | Path | Description |
|---|---|---|
| `GET` | `/reports/lead-source-conversion` | Conversion rate by `Lead.source` (`FR-CRM-054`). |
| `GET` | `/reports/customers-by-product-status?product_id=&status=` | "Which customers use Product X" / "have a Project in status Y" (`FR-CRM-056`) — do not confuse with the `business_unit`/`channel` filters in §9, which are lightweight Deal tags, not this real relationship query. |

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
    "avg_deal_size": 185000,
    "avg_sales_cycle_days": 34,
    "pipeline_coverage_ratio": 1.6,
    "quarterly_sales_target": 3000000,
    "revenue_trend": [ { "label": "Mar", "value": 320000 }, "...6 months" ],
    "stage_breakdown": [ { "stage": "Qualified", "value": 900000, "count": 4 }, "...per DealStage" ],
    "industry_breakdown": [ { "industry": "Retail", "win_rate": 55, "won_count": 6 }, "..." ],
    "team_performance": [ { "user_id": 3, "name": "...", "won_count": 5, "won_value": 620000, "win_rate": 60 }, "..." ],
    "upsell_opportunities": [ /* stale-contact candidates grouped by tier, see FR in dashboard hint copy */ ]
  }
}
```

`quarterly_sales_target` should move server-side from the frontend's hardcoded `QUARTERLY_SALES_TARGET` constant once Admin-configurable quotas exist (`FR-CRM-058`, currently 🚧) — expose it as a value in this response either way so the frontend stops hardcoding it the moment the backend is live, even before an Admin settings UI for it exists.

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

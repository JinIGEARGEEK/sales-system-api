# Open API — User Manual

A guide for external/partner integrations that need to create, read, or update **Company**, **Contact**, **Project**, **Product**, **Prospect**, and **Lead** records without a staff login. If you're working inside this repo on the main resource API instead, see [`biz_spec/api-system-spec.md`](../biz_spec/api-system-spec.md) — this document only covers the `/open/*` routes and the `/admin/api-keys` credentials that unlock them (§8.9 there).

This CRM is meant to be the **source of truth** for this data across our internal systems — several of them create, update, and read the same records here. Two things follow from that, both covered in detail below: every Company Create is deduped by website domain so the same real-world company never ends up as two rows (§11's `409 Conflict`), and every Create across every resource supports an `Idempotency-Key` header so a retried call can't accidentally create a duplicate either (§11).

---

## 1. How it works

- Every call is authenticated with an **API key** sent in the `X-API-Key` header — not the `Authorization: Bearer <JWT>` staff login flow used elsewhere in this API.
- A key **acts as** one specific staff user (its "owner"). Anything you create or update through the Open API is attributed to that person (`created_by`/`updated_by`), exactly as if they'd made the change themselves.
- Six resources are exposed this way — **Company**, **Contact**, **Project**, **Product**, **Prospect**, and **Lead** — and only four operations per resource: **list, create, get, update**. There is no delete, trash, restore, bulk, or convert endpoint on the Open API, regardless of what the owner's own staff account could otherwise do.
- **A key can read/write every record of these types in the system, not just ones its owner created.** There's no per-key or per-owner data partition — if you issue keys to more than one external partner, each one can see and modify every other partner's records too. Plan key issuance accordingly (§2) if that matters for your integration.
- **Prospect/Lead's ownership rule (`CanWrite`) applies to create/update only, not list/get.** A key acting as a Sales Rep can only *create or update* a Prospect/Lead that's unassigned or already assigned to that same rep (a key acting as Admin or Sales Manager can write any of them) — but `GET /open/prospects`, `GET /open/leads`, and their `/:id` counterparts return every Prospect/Lead in the system regardless of who it's assigned to, for every key, the same "no per-row ownership filter on reads" behavior the staff app's own `/prospects`/`/leads` List and Get already have. Pick your key's `owner_user_id` (§2) with the write-side restriction in mind — it doesn't limit what that key can read.
- Only an **Admin** can issue or revoke keys (§2 below). If you're an external integrator, get your key from whoever administers this CRM for your organization — you cannot self-serve one.

## 2. Getting a key (Admin only)

Only an Admin can issue a key — if you're an external integrator, ask whoever administers this CRM for your organization to create one for you and hand you the raw key out of band (Slack DM, a password manager share, etc.). There's no self-serve signup.

### 2a. Via the CRM UI (recommended)

The `sales-system` frontend has a dedicated screen for this:

1. Log in as an Admin.
2. Open the sidebar's **Settings** group → **API Keys** (`/admin/api-keys`).
3. Click **Add key**, fill in a **Name** (anything descriptive, e.g. "Acme Marketing Sync") and pick an **Acts as** owner from the dropdown — only active staff accounts are selectable, and this is the identity every one of this key's calls will be attributed to (`created_by`/`updated_by`).
4. Submit. A **"reveal" dialog pops up showing the raw key exactly once**, with a copy-to-clipboard button — copy it into your secrets manager *before* clicking Done. The dialog can't be dismissed by clicking outside it, only by the Done button, so you don't accidentally close it before copying.
5. The key now shows up in the table (Name / Key prefix / Acts As / Status / Last Used / Created), with a **Revoke** action per row.

### 2b. Via the API directly

Equivalent to the UI flow above, for scripting or if you don't have frontend access:

```
POST /api/v1/admin/api-keys
Authorization: Bearer <admin's JWT>
Content-Type: application/json

{
  "name": "Acme Marketing Sync",
  "owner_user_id": 7
}
```

`owner_user_id` must be an existing, active staff user — the identity this key will act as on every Open API call.

```json
{
  "data": {
    "api_key": {
      "id": 3,
      "name": "Acme Marketing Sync",
      "key_prefix": "sk_live_9f2a1c",
      "owner_user_id": 7,
      "is_active": true,
      "last_used_at": null,
      "revoked_at": null,
      "revoked_by": null,
      "created_at": "2026-09-11T09:00:00Z"
    },
    "key": "sk_live_<REDACTED-64-HEX-CHARS-SHOWN-ONLY-ONCE-HERE>"
  }
}
```

> **`key` is shown exactly once, right here.** Only its hash is stored — there is no "forgot my key" recovery. Save it in your secrets manager immediately; if it's lost, revoke it and issue a new one.

**List keys** (metadata only — the raw key is never returned again):

```
GET /api/v1/admin/api-keys
Authorization: Bearer <admin's JWT>
```

**Revoke a key** (immediate; instance caches catch up within 30s at the latest):

```
POST /api/v1/admin/api-keys/3/revoke
Authorization: Bearer <admin's JWT>
```

Revoking sets `is_active: false` and records `revoked_at`/`revoked_by` — the row (and its history) is kept, not deleted. Same effect as clicking **Revoke** on that key's row in the UI table.

**See a key's write history** (Admin only — who/what changed via this specific key, distinct from `/audit-log`'s per-*owner* view since one owner can hold several keys):

```
GET /api/v1/admin/api-keys/3/logs
Authorization: Bearer <admin's JWT>
```

Returns a paginated list of this key's `POST`/`PUT`/`PATCH` calls against every `/open/*` resource — method, path, resource type/id, status code, timestamp. Read-only (`GET`) calls aren't logged.

## 3. Authenticating your requests

Send your key on every Open API call:

```
X-API-Key: sk_live_<REDACTED-64-HEX-CHARS-SHOWN-ONLY-ONCE-HERE>
```

| Situation | Response |
|---|---|
| Header missing | `401 Unauthorized` — `{"error":{"code":"UNAUTHORIZED","message":"Missing X-API-Key header"}}` |
| Key unknown, malformed, or revoked | `401 Unauthorized` — `{"error":{"code":"UNAUTHORIZED","message":"Invalid API key"}}` |
| Owner account since deactivated | Same as above — a key stops working the moment its owner is deactivated |

## 4. Rate limits

**300 requests/minute per key.** Past that, every endpoint returns:

```
429 Too Many Requests
{"error":{"code":"TOO_MANY_REQUESTS","message":"Too many requests — try again shortly"}}
```

The limit is per-key (keyed on the authenticated key's own id, not the raw header text), not per source IP — safe to call from a shared egress IP (a server, a serverless function pool, etc.) without one integration's traffic capping another's. Authentication is checked *before* the rate limit, so an invalid/unknown key is always rejected with `401` rather than ever counting against (or being limited by) anyone's `300`/minute budget.

## 5. Discovering valid option values

`size`, `revenue_size`, and `role_title` (§6/§7 below) must each match one of this account's admin-configured active options — and since those are tuned per-deployment, don't hardcode the seeded defaults shown later in this doc as if they were fixed. Rather than asking an Admin out of band every time the list changes, read them straight from the API:

```
GET /api/v1/open/options
X-API-Key: sk_live_...
```

```json
{
  "data": {
    "industries": ["Education", "Finance", "Healthcare", "Manufacturing", "Retail", "Technology"],
    "sizes": ["1-10", "11-50", "51-200", "201-500", "501-1000", "1000+"],
    "revenue_sizes": ["< 1M THB", "1M - 5M THB", "5M - 20M THB", "20M - 100M THB", "100M+ THB"],
    "job_titles": ["CEO", "Director", "Manager", "Owner", "Staff", "Other"]
  }
}
```

`industries` is shown for reference only — `industry` is free text (§6) and any value is still accepted, auto-registering itself as a new option if it isn't one of these already. `sizes`/`revenue_sizes`/`job_titles` are strictly enforced: a Create/Update with a value not in this list is rejected (§13's `422`).

## 6. Companies

### `GET /api/v1/open/companies` — List

Supports the same filters as the staff-facing list: `status`, `tag`, `industry`, `search` (matches name), `stale_days`, `has_won_deal`, `sort` (`created_at`/`name`/`industry`, prefix `-` for descending), `page`, `per_page`. `status`, `tag`, and `industry` all match case-insensitively (`?status=ACTIVE` and `?status=active` behave identically).

```
GET /api/v1/open/companies?search=acme&status=active
X-API-Key: sk_live_...
```

```json
{
  "data": [
    {
      "id": 42,
      "name": "Acme Corp",
      "industry": "Retail",
      "size": "51-200",
      "revenue_size": "1M - 5M THB",
      "website": "https://acme.example.com",
      "tags": ["vip"],
      "notes": "",
      "status": "active",
      "legal_name": null,
      "address": null,
      "tax_id": null,
      "created_at": "2026-08-01T10:00:00Z",
      "updated_at": "2026-08-01T10:00:00Z",
      "created_by": 7,
      "updated_by": 7,
      "last_activity_at": "2026-09-01T12:00:00Z"
    }
  ],
  "page": 1, "per_page": 20, "total": 1, "total_page": 1, "next": null, "prev": null
}
```

### `POST /api/v1/open/companies` — Create

```
POST /api/v1/open/companies
X-API-Key: sk_live_...
Content-Type: application/json

{
  "name": "Acme Corp",
  "industry": "Retail",
  "size": "51-200",
  "revenue_size": "1M - 5M THB",
  "website": "https://acme.example.com",
  "tags": ["vip"],
  "notes": "Introduced via trade show",
  "status": "active"
}
```

Every field below is a **JSON string** unless noted otherwise — `tags` is an array of strings, and `legal_name`/`address`/`tax_id` additionally accept explicit `null`. Sending the wrong JSON type (a number for `revenue_size`, an object for `tags`, etc.) fails to parse at all and returns `400 Bad Request` — see [§13](#13-error-reference) — not the `422` used for a missing/invalid value.

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | ✅ | |
| `industry` | string | | Free text, matched case-insensitively on `?industry=` — a new value (in any casing) is auto-registered rather than rejected. See §5 for the current list. |
| `website` | string | | A lenient domain/URL check — "not a website", empty text, or similar obvious garbage is rejected (`422`); real-world quirky-but-valid domains are not. **Deduped**: Create/Update reject (`409 Conflict`) a website whose domain already belongs to a different Company — see §12. |
| `tags` | string[] | | e.g. `["vip", "renewed"]`. Normalized on write (trimmed, lowercased, de-duplicated) and matched case-insensitively on `?tag=` — sending `"VIP"` and `"vip"` on different calls results in one stored tag, not two. |
| `size` | string | | Must exactly match one of this account's active company-size options — see §5 for the current list. |
| `revenue_size` | string | | Same rule as `size` — see §5. **Common mistake:** sending a number (e.g. `3`) or a bare numeric string instead of one of the label strings §5 returns — that's a type/value mismatch, not a valid shorthand. |
| `notes` | string | | |
| `status` | string | | `"active"` or `"archived"`, matched/stored case-insensitively (`"Active"` is accepted and normalized to `"active"`); defaults to `active`. |
| `legal_name`, `address`, `tax_id` | string \| null | | Used on Contract PDF exports if present. |

`201 Created` returns the new Company (same shape as List's rows, minus `last_activity_at`). `422 Unprocessable Entity` for a missing `name`, an invalid `website`, or a `size`/`revenue_size`/`status` that doesn't match an active option/allowed value. `409 Conflict` if the website's domain already belongs to a different Company (§12). `400 Bad Request` for a field sent as the wrong JSON type — see [§13](#13-error-reference).

### `GET /api/v1/open/companies/:id` — Get

```
GET /api/v1/open/companies/42
X-API-Key: sk_live_...
```

`404 Not Found` if the id doesn't exist (or was soft-deleted — the Open API can't restore it; ask an Admin).

### `PUT /api/v1/open/companies/:id` — Update

Same body shape and validation as Create (`name` is required here too — an update can't blank it out). **Almost every field is replaced outright — an omitted field is cleared, not left unchanged.** `status` is the one exception: omit it (or send `""`) and the existing status is kept, since an empty string is never a valid status to set. Everything else (`name`, `industry`, `size`, `revenue_size`, `website`, `tags`, `notes`, `legal_name`, `address`, `tax_id`) follows the general rule — resend the current value for anything you don't intend to blank out. Changing `website` to a domain already used by a *different* Company gets the same `409 Conflict` Create does (§12) — changing it back to the Company's own current domain is fine.

```
PUT /api/v1/open/companies/42
X-API-Key: sk_live_...
Content-Type: application/json

{
  "name": "Acme Corp",
  "industry": "Retail",
  "size": "51-200",
  "revenue_size": "1M - 5M THB",
  "website": "https://acme.example.com",
  "tags": ["vip", "renewed"],
  "notes": "Renewed for 2027",
  "status": "active"
}
```

## 7. Contacts

### `GET /api/v1/open/contacts` — List

Filters: `company_id`, `status`, `tag`, `search` (name/email), `sort` (`created_at`/`name`/`email`/`company_name`), `page`, `per_page`. `status` and `tag` match case-insensitively, same as Companies.

```
GET /api/v1/open/contacts?company_id=42
X-API-Key: sk_live_...
```

```json
{
  "data": [
    {
      "id": 101,
      "company_id": 42,
      "name": "Jane Doe",
      "email": "jane@acme.example.com",
      "phone": "+66-2-000-0000",
      "role_title": "Purchasing Manager",
      "tags": [],
      "status": "active",
      "is_primary": true,
      "created_at": "2026-08-01T10:05:00Z",
      "updated_at": "2026-08-01T10:05:00Z",
      "created_by": 7,
      "updated_by": 7
    }
  ],
  "page": 1, "per_page": 20, "total": 1, "total_page": 1, "next": null, "prev": null
}
```

### `POST /api/v1/open/contacts` — Create

```
POST /api/v1/open/contacts
X-API-Key: sk_live_...
Content-Type: application/json

{
  "company_id": 42,
  "name": "Jane Doe",
  "email": "jane@acme.example.com",
  "phone": "+66-2-000-0000",
  "role_title": "Purchasing Manager",
  "is_primary": true
}
```

Every field is a **JSON string** except `company_id` (integer), `tags` (array of strings), and `is_primary` (boolean) — see [§6's note on wrong-type requests](#6-companies).

| Field | Type | Required | Notes |
|---|---|---|---|
| `company_id` | integer | ✅ | Must reference an existing Company's numeric `id` — not a string, not the company name. |
| `name` | string | ✅ | |
| `email` | string | | A lenient email-format check (`someone@somewhere.tld`) — empty is fine, garbage is rejected (`422`). |
| `phone` | string | | A lenient check — digits, spaces, `+`, `-`, `(`, `)`, 6-20 chars; empty is fine. |
| `tags` | string[] | | Normalized/matched the same way as Company tags (§6). |
| `role_title` | string | | Must exactly match one of this account's active job-title options — see §5 for the current list. |
| `status` | string | | `"active"` or `"archived"`, matched/stored case-insensitively; defaults to `active`. |
| `is_primary` | boolean | | `true`/`false` JSON boolean. At most one Contact per Company can be primary — setting this on one automatically un-sets it on any other Contact of the same Company. |

`201 Created` on success. `422` if `company_id`/`name` is missing, `email`/`phone` isn't a valid format, or `role_title`/`status` doesn't match an active option/allowed value. `400 Bad Request` for a field sent as the wrong JSON type (a string for `company_id`, a number for `is_primary`, etc.).

### `GET /api/v1/open/contacts/:id` — Get

```
GET /api/v1/open/contacts/101
X-API-Key: sk_live_...
```

### `PUT /api/v1/open/contacts/:id` — Update

Same body shape and validation as Create (`name` is required here too), with the same general rule as Company Update (an omitted field is cleared, not preserved) and **two exceptions** worth calling out explicitly:

- `company_id` — omit it (or send `0`) and the Contact keeps its current Company; it cannot be blanked out this way.
- `status` — same as Company: omit it (or send `""`) and the current status is kept.

Everything else, including **`is_primary`, is a true full replace** — this is the field most likely to bite you: omitting it (or sending `false`) on an update to an already-primary Contact will un-set `is_primary`, since the field has no "leave unchanged" default. Always send the Contact's current `is_primary` value explicitly if you're not deliberately changing it.

```
PUT /api/v1/open/contacts/101
X-API-Key: sk_live_...
Content-Type: application/json

{
  "company_id": 42,
  "name": "Jane Doe",
  "email": "jane.doe@acme.example.com",
  "phone": "+66-2-000-0000",
  "role_title": "Purchasing Manager",
  "is_primary": true
}
```

## 8. Projects

A Project belongs to a Company — unlike the staff app's own nested `POST /companies/:companyId/projects`, the Open API's Create takes `company_id` in the body instead of the path, since there's no company-scoped route segment here.

### `GET /api/v1/open/projects` — List

Filters: `status`, `company_id` (exact match), `sort` (`created_at`/`name`/`target_end_date`, prefix `-` for descending), `page`, `per_page`.

```
GET /api/v1/open/projects?company_id=42
X-API-Key: sk_live_...
```

```json
{
  "data": [
    {
      "id": 7,
      "company_id": 42,
      "company_name": "Acme Corp",
      "deal_id": null,
      "name": "Website Revamp",
      "status": "Not Started",
      "start_date": "2026-09-01T00:00:00Z",
      "target_end_date": null,
      "expected_proposal_date": null,
      "expected_start_date": null,
      "production_reference": null,
      "notes": "",
      "created_at": "2026-09-01T10:00:00Z",
      "updated_at": "2026-09-01T10:00:00Z",
      "created_by": 7,
      "updated_by": 7
    }
  ],
  "page": 1, "per_page": 20, "total": 1, "total_page": 1, "next": null, "prev": null
}
```

### `POST /api/v1/open/projects` — Create

```
POST /api/v1/open/projects
X-API-Key: sk_live_...
Content-Type: application/json

{
  "company_id": 42,
  "name": "Website Revamp",
  "status": "Not Started",
  "notes": "Kicked off after Deal #19 closed"
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `company_id` | integer | ✅ | Must reference an existing Company's numeric `id`. |
| `name` | string | ✅ | |
| `deal_id` | integer \| null | | Optional link back to the Deal this Project came from. |
| `status` | string | | One of `Not Started`, `In Progress`, `On Hold`, `Completed`, `Cancelled`. Defaults to `Not Started` when omitted. |
| `start_date` | string (RFC3339) | | Defaults to the current time when omitted. |
| `target_end_date`, `expected_proposal_date`, `expected_start_date` | string (RFC3339) \| null | | |
| `production_reference` | string \| null | | |
| `notes` | string | | |

`201 Created` on success. `404 Not Found` if `company_id` doesn't reference an existing Company. `422 Unprocessable Entity` if `company_id` or `name` is missing. `400 Bad Request` for a field sent as the wrong JSON type.

### `GET /api/v1/open/projects/:id` — Get

```
GET /api/v1/open/projects/7
X-API-Key: sk_live_...
```

`404 Not Found` if the id doesn't exist (or was soft-deleted).

### `PATCH /api/v1/open/projects/:id` — Update

Unlike Company/Contact's full-replace `PUT`, this is a **partial update** — only the fields you include in the body are changed; anything omitted is left as-is. `company_id` cannot be changed via Update.

```
PATCH /api/v1/open/projects/7
X-API-Key: sk_live_...
Content-Type: application/json

{ "status": "In Progress" }
```

`200 OK` on success. `404 Not Found` if the id doesn't exist.

## 9. Products

Products form a flat catalog — not scoped to any Company (the Company↔Product link, `CustomerProduct`, isn't exposed on the Open API).

### `GET /api/v1/open/products` — List

Filters: `category`, `search` (matches name, substring), `sort` (`created_at`/`name`), `page`, `per_page`.

```
GET /api/v1/open/products?category=Software
X-API-Key: sk_live_...
```

```json
{
  "data": [
    {
      "id": 3,
      "name": "CRM Pro License",
      "category": "Software",
      "description": "Annual seat license",
      "price": 12000,
      "is_active": true,
      "created_at": "2026-08-01T10:00:00Z",
      "updated_at": "2026-08-01T10:00:00Z",
      "created_by": 7,
      "updated_by": 7
    }
  ],
  "page": 1, "per_page": 20, "total": 1, "total_page": 1, "next": null, "prev": null
}
```

### `POST /api/v1/open/products` — Create

```
POST /api/v1/open/products
X-API-Key: sk_live_...
Content-Type: application/json

{ "name": "CRM Pro License", "category": "Software", "price": 12000 }
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | ✅ | |
| `category` | string | ✅ | Must exactly match one of this account's active product-category options. |
| `description` | string | | |
| `price` | number | | |
| `is_active` | boolean | | Defaults to `true` when omitted. |

`201 Created` on success. `422 Unprocessable Entity` if `name` is missing or `category` isn't an active option. `400 Bad Request` for a field sent as the wrong JSON type.

### `GET /api/v1/open/products/:id` — Get

```
GET /api/v1/open/products/3
X-API-Key: sk_live_...
```

`404 Not Found` if the id doesn't exist (or was soft-deleted).

### `PATCH /api/v1/open/products/:id` — Update

**Full replace of the catalog entry's own fields** (not a partial update like Project's above) — resend `name`/`category`/`description`/`price`/`is_active` even for fields you aren't changing. Deactivating a Product (the "remove from catalog" action) is just `is_active: false` here — there's no separate deactivate endpoint on the Open API.

```
PATCH /api/v1/open/products/3
X-API-Key: sk_live_...
Content-Type: application/json

{ "name": "CRM Pro License", "category": "Software", "price": 15000, "is_active": true }
```

`200 OK` on success. `404 Not Found` if the id doesn't exist. `422 Unprocessable Entity` if `name` is missing or `category` isn't an active option.

## 10. Prospects

The pre-Lead marketing funnel entity — not necessarily linked to a Company yet.

### `GET /api/v1/open/prospects` — List

Filters: `status`, `source`, `assigned_to` (user id, or `"unassigned"`), `company_id`, `search` (name/email/company name), `exclude_converted` (`"true"` to hide already-converted Prospects), `sort` (`created_at`/`name`), `page`, `per_page`.

```
GET /api/v1/open/prospects?status=New
X-API-Key: sk_live_...
```

```json
{
  "data": [
    {
      "id": 55,
      "name": "Riley Chen",
      "company_id": null,
      "email": "riley@example.com",
      "phone": "",
      "source": "Social Media",
      "status": "New",
      "notes": "",
      "assigned_to": null,
      "tags": [],
      "converted_lead_id": null,
      "business_unit": null,
      "business_unit_item": null,
      "created_at": "2026-09-01T10:00:00Z",
      "updated_at": "2026-09-01T10:00:00Z",
      "created_by": 7,
      "updated_by": 7
    }
  ],
  "page": 1, "per_page": 20, "total": 1, "total_page": 1, "next": null, "prev": null
}
```

### `POST /api/v1/open/prospects` — Create

```
POST /api/v1/open/prospects
X-API-Key: sk_live_...
Content-Type: application/json

{ "name": "Riley Chen", "email": "riley@example.com", "source": "Social Media" }
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | ✅ | |
| `company_id` | integer \| null | | Existing Company to link, if known. |
| `email` | string | | Lenient format check; empty is fine, garbage is rejected (`422`). |
| `phone` | string | | |
| `source` | string | | Must exactly match one of this account's active Prospect-source options (a *separate* list from Lead's own sources — see §5-style discovery via `GET /admin/prospect-sources`, staff login only; not currently mirrored under `/open/options`). |
| `status` | string | | Must be an active Prospect stage. Defaults to `New` when omitted. **`"Converted"` can never be set directly** — that's system-managed via the staff app's Convert action, which isn't exposed on the Open API. |
| `notes` | string | | |
| `assigned_to` | integer \| null | | See §1's ownership note — a key acting as a Sales Rep can only assign to themselves. |
| `tags` | string[] | | |
| `business_unit` | string \| null | | `"Project"` or `"Product"`. |
| `business_unit_item` | string \| null | | |

`201 Created` on success. `422 Unprocessable Entity` for a missing `name`, an invalid `email`, a `source`/`status`/`business_unit` that isn't valid, or an attempt to set `status: "Converted"` directly. `403 Forbidden` if `assigned_to` names someone other than the key's own owner while acting as a Sales Rep.

### `GET /api/v1/open/prospects/:id` — Get

```
GET /api/v1/open/prospects/55
X-API-Key: sk_live_...
```

`404 Not Found` if the id doesn't exist (or was soft-deleted).

### `PUT /api/v1/open/prospects/:id` — Update

Same body shape and validation as Create — a **full replace**, same as Company/Contact's Update: an omitted field is cleared, not left unchanged.

```
PUT /api/v1/open/prospects/55
X-API-Key: sk_live_...
Content-Type: application/json

{ "name": "Riley Chen", "email": "riley@example.com", "source": "Social Media", "status": "Engaging" }
```

`200 OK` on success. `403 Forbidden` if the key's owner doesn't own this Prospect (Sales Rep) or is reassigning it to someone else. `404 Not Found` if the id doesn't exist.

## 11. Leads

Leads are Sales's own pipeline entity — created directly, or via a Prospect conversion in the staff app (not exposed here).

### `GET /api/v1/open/leads` — List

Filters: `status`, `source`, `assigned_to` (user id, or `"unassigned"`), `company_id`, `search` (name/email/company name), `exclude_converted` (`"true"`), `sort` (`created_at`/`name`), `page`, `per_page`.

```
GET /api/v1/open/leads?status=New
X-API-Key: sk_live_...
```

```json
{
  "data": [
    {
      "id": 88,
      "name": "Jordan Lee",
      "company_id": null,
      "email": "jordan@example.com",
      "phone": "",
      "source": "Referral",
      "status": "New",
      "notes": "",
      "assigned_to": 7,
      "tags": [],
      "converted_deal_id": null,
      "prospect_id": null,
      "score": 0,
      "classification": "none",
      "business_unit": null,
      "business_unit_item": null,
      "created_at": "2026-09-01T10:00:00Z",
      "updated_at": "2026-09-01T10:00:00Z",
      "created_by": 7,
      "updated_by": 7
    }
  ],
  "page": 1, "per_page": 20, "total": 1, "total_page": 1, "next": null, "prev": null
}
```

### `POST /api/v1/open/leads` — Create

```
POST /api/v1/open/leads
X-API-Key: sk_live_...
Content-Type: application/json

{ "name": "Jordan Lee", "email": "jordan@example.com", "source": "Referral" }
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | ✅ | |
| `company_id` | integer \| null | | Existing Company to link, if known. |
| `email` | string | | Lenient format check. |
| `phone` | string | | |
| `source` | string | | Must exactly match one of this account's active Lead-source options — see §5's `GET /open/options` for `industries`/`sizes`/`revenue_sizes`/`job_titles` only; Lead source itself isn't in that list yet, use `GET /admin/lead-sources` (staff login) to discover it. |
| `status` | string | | Defaults to `New` when omitted. |
| `notes` | string | | |
| `assigned_to` | integer \| null | | See §1's ownership note. If omitted entirely, the Lead is auto-assigned round-robin among active Sales Reps by current open-record load — same as the staff app's own Create. |
| `classification` | string | | Only `"sql"` is honored as an explicit override; any other value (including omitted) is auto-computed from the Lead's score. |
| `business_unit` | string \| null | | `"Project"` or `"Product"`. |
| `business_unit_item` | string \| null | | |

`201 Created` on success. `422 Unprocessable Entity` for a missing `name`, invalid `email`, or invalid `source`/`business_unit`. `403 Forbidden` if `assigned_to` names someone other than the key's own owner while acting as a Sales Rep.

### `GET /api/v1/open/leads/:id` — Get

```
GET /api/v1/open/leads/88
X-API-Key: sk_live_...
```

`404 Not Found` if the id doesn't exist (or was soft-deleted).

### `PUT /api/v1/open/leads/:id` — Update

Same body shape and validation as Create — a **full replace**. Omitting `classification` leaves an existing manual `"sql"` override in place rather than letting it auto-recompute away; an explicit `"sql"` always wins.

```
PUT /api/v1/open/leads/88
X-API-Key: sk_live_...
Content-Type: application/json

{ "name": "Jordan Lee", "email": "jordan@example.com", "source": "Referral", "status": "Contacted" }
```

`200 OK` on success. `403 Forbidden` if the key's owner doesn't own this Lead (Sales Rep) or is reassigning it to someone else. `404 Not Found` if the id doesn't exist.

## 12. Avoiding duplicates (idempotent retries + domain dedupe)

This CRM is the source of truth other internal systems sync this data through, so an accidental duplicate isn't just clutter here — it propagates to everything reading from it. Two independent safeguards:

### 12a. `Idempotency-Key` — safe retries

A network timeout or a dropped response leaves you not knowing whether your `POST` actually went through. Send an `Idempotency-Key` header (any string you generate — a UUID per logical operation is typical) on any `POST /open/*` Create endpoint (companies, contacts, projects, products, prospects, leads), and retrying with the *same* key + the *same* request body replays the original response instead of creating a second row:

```
POST /api/v1/open/companies
X-API-Key: sk_live_...
Idempotency-Key: 6b1b6e6a-2f7a-4b3e-9c1a-7e8f2a1b9c3d
Content-Type: application/json

{ "name": "Acme Corp" }
```

- **Same key, same body, retried** → the original `201` (or `422`/etc.) response is returned again verbatim; no new row is created.
- **Same key, a DIFFERENT body** → `409 Conflict` — you've reused a key for two different requests, which is rejected outright rather than silently picking one.
- **A request with that key still in flight** (you fired two copies at once) → `409 Conflict` on whichever one loses the race, rather than both proceeding.
- **No `Idempotency-Key` header at all** → works exactly as before this existed; it's opt-in.

Idempotency keys are scoped per API key and stay valid for replay for 24 hours after the original attempt — a truly new call should use a fresh key value (don't reuse one across unrelated operations).

### 12b. Domain dedupe on Company Create/Update

Independent of idempotency keys: `POST /open/companies` (and `PUT` when changing `website`) checks whether the website's domain already belongs to a *different*, existing Company — `https://acme.com`, `http://www.acme.com/about`, and `acme.com` all normalize to the same domain. If it does, you get `409 Conflict` naming the existing Company's id instead of a second row being silently created:

```json
{ "error": { "code": "CONFLICT", "message": "A company with this website already exists (id 42, \"Acme Corp\")" } }
```

On a `409` here, `GET`/`PUT` the existing id rather than retrying Create — that's almost certainly the same real-world company your system already has under a different name/spelling. A Company with no `website` (or one whose domain doesn't already exist elsewhere) is unaffected. **No such domain-dedupe exists for Project/Product/Prospect/Lead** — only the Idempotency-Key safeguard above protects those from a retried Create.

## 13. Error reference

Every error follows the same envelope:

```json
{ "error": { "code": "VALIDATION_ERROR", "message": "name is required", "fields": { "name": ["required"] } } }
```

`fields` is only present on `422` responses. A `400` (wrong JSON type / unparseable body) looks like this instead — no `fields` map, and the message doesn't name the offending field, so double-check every field's type against §6-§11's field tables when you see it:

```json
{ "error": { "code": "BAD_REQUEST", "message": "Invalid request body" } }
```

| Status | `code` | When |
|---|---|---|
| 400 | `BAD_REQUEST` | Request body isn't valid JSON, or a field's JSON type doesn't match what's expected (e.g. `revenue_size` sent as a number instead of a string, `company_id` sent as a string instead of a number, `tags` sent as a single string instead of an array). This happens *before* any field-level validation runs, so the response has no `fields` map — check every field's type against the tables in [§6](#6-companies) through [§11](#11-leads). |
| 401 | `UNAUTHORIZED` | Missing/invalid/revoked API key |
| 403 | `FORBIDDEN` | The key's owner (a Sales Rep) tried to create/update/assign a Prospect or Lead they don't own — see §1's ownership note |
| 404 | `NOT_FOUND` | The record id doesn't exist — or, on Project Create, `company_id` doesn't reference an existing Company |
| 409 | `CONFLICT` | A Company's website domain already belongs to a different Company (§12b); or an `Idempotency-Key` was reused with a different body, or while its original request is still in flight (§12a) |
| 422 | `VALIDATION_ERROR` | Missing required field, invalid `website`/`email`/`phone` format, invalid `status`, or a `size`/`revenue_size`/`role_title`/`category`/`source`/`business_unit` that isn't a valid/active option |
| 429 | `TOO_MANY_REQUESTS` | Over 300 requests/minute on this key |
| 500 | `INTERNAL_ERROR` | Unexpected server error — safe to retry (pair with an `Idempotency-Key`, §12a, on a Create so a retry after a `500` can't double-create) |

## 14. Quick start (curl)

```sh
API_KEY="sk_live_..."
BASE="https://<your-domain>/api/v1"

# See what size/revenue_size/role_title/industry values are currently valid
curl -sS "$BASE/open/options" -H "X-API-Key: $API_KEY"

# Create a Company (Idempotency-Key makes this retry-safe)
curl -sS -X POST "$BASE/open/companies" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"name":"Acme Corp","industry":"Retail"}'

# Create a Contact under it
curl -sS -X POST "$BASE/open/contacts" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"company_id":42,"name":"Jane Doe","email":"jane@acme.example.com"}'

# Look one up
curl -sS "$BASE/open/companies/42" -H "X-API-Key: $API_KEY"

# Create a Project under the Company (company_id in the body, not the path)
curl -sS -X POST "$BASE/open/projects" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"company_id":42,"name":"Website Revamp"}'

# Create a Prospect
curl -sS -X POST "$BASE/open/prospects" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"name":"Riley Chen","source":"Social Media"}'

# Create a Lead
curl -sS -X POST "$BASE/open/leads" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"name":"Jordan Lee","source":"Referral"}'
```

## 15. FAQ

**Can I delete a record through this API?** No — Open API scope is create/read/update only, on Company/Contact/Project/Product/Prospect/Lead. Ask an Admin to do it through the staff app.

**Can my key see other resources (Deals, Customer-Products, …)?** No — only `/open/companies`, `/open/contacts`, `/open/projects`, `/open/products`, `/open/prospects`, `/open/leads`, and `/open/options` accept `X-API-Key`; every other route still requires the staff Bearer-JWT login. There's also no Convert endpoint (Prospect→Lead, Lead→Deal) on the Open API — that's staff-app only.

**Can my key see/modify records another integration created?** Yes for Company/Contact/Project/Product — see §1: there's no per-key data partition. For Prospect/Lead, a key acting as a Sales Rep is still restricted to records assigned to that rep or unassigned (§1); a key acting as Admin/Sales Manager can see/modify any of them.

**What happens if the staff user my key acts as gets deactivated?** The key stops working immediately (within the 30s cache window) — reactivate that user or point the key at a different `owner_user_id` (issue a new key; a key's owner can't be changed after creation).

**Is there a sandbox/test key?** Not currently — every key acts against the same live data as the staff app it's paired with.

**How do I tell which requests came from a given key?** `GET /admin/api-keys/:id/logs` (Admin, staff login — §2) lists that key's write history.

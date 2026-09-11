# Open API — User Manual

A guide for external/partner integrations that need to create, read, or update **Company** and **Contact** records without a staff login. If you're working inside this repo on the main resource API instead, see [`biz_spec/api-system-spec.md`](../biz_spec/api-system-spec.md) — this document only covers the `/open/*` routes and the `/admin/api-keys` credentials that unlock them (§8.9 there).

---

## 1. How it works

- Every call is authenticated with an **API key** sent in the `X-API-Key` header — not the `Authorization: Bearer <JWT>` staff login flow used elsewhere in this API.
- A key **acts as** one specific staff user (its "owner"). Anything you create or update through the Open API is attributed to that person (`created_by`/`updated_by`), exactly as if they'd made the change themselves.
- Only two resources are exposed this way — **Company** and **Contact** — and only four operations: **list, create, get, update**. There is no delete, trash, or bulk endpoint on the Open API, regardless of what the owner's own staff account could otherwise do.
- Only an **Admin** can issue or revoke keys (§2 below). If you're an external integrator, get your key from whoever administers this CRM for your organization — you cannot self-serve one.

## 2. Getting a key (Admin only)

This part requires a normal staff login (`POST /auth/login`, Admin role) — it's how an Admin issues a key for you, not something you do yourself.

**Create a key:**

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

Revoking sets `is_active: false` and records `revoked_at`/`revoked_by` — the row (and its history) is kept, not deleted.

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

The limit is per-key, not per source IP — safe to call from a shared egress IP (a server, a serverless function pool, etc.) without one integration's traffic capping another's.

## 5. Companies

### `GET /api/v1/open/companies` — List

Supports the same filters as the staff-facing list: `status`, `tag`, `industry`, `search` (matches name), `stale_days`, `has_won_deal`, `sort` (`created_at`/`name`/`industry`, prefix `-` for descending), `page`, `per_page`.

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
      "revenue_size": "10M-50M",
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
  "revenue_size": "10M-50M",
  "website": "https://acme.example.com",
  "tags": ["vip"],
  "notes": "Introduced via trade show",
  "status": "active"
}
```

| Field | Required | Notes |
|---|---|---|
| `name` | ✅ | |
| `industry` | | Free text — a new value is auto-registered rather than rejected. |
| `size` | | Must match one of this account's active company-size options (ask your Admin for the current list — `/admin/company-sizes` needs a staff login to read). Omit if unsure. |
| `revenue_size` | | Same as `size`, against `/admin/revenue-sizes`. |
| `website`, `tags`, `notes` | | |
| `status` | | `active` or `archived`; defaults to `active`. |
| `legal_name`, `address`, `tax_id` | | Used on Contract PDF exports if present. |

`201 Created` returns the new Company (same shape as List's rows, minus `last_activity_at`). A missing `name`, or a `size`/`revenue_size` that doesn't match an active option, returns `422 Unprocessable Entity` with a `fields` map naming the offending key.

### `GET /api/v1/open/companies/:id` — Get

```
GET /api/v1/open/companies/42
X-API-Key: sk_live_...
```

`404 Not Found` if the id doesn't exist (or was soft-deleted — the Open API can't restore it; ask an Admin).

### `PUT /api/v1/open/companies/:id` — Update

Same body shape as Create. **Almost every field is replaced outright — an omitted field is cleared, not left unchanged.** `status` is the one exception: omit it (or send `""`) and the existing status is kept, since an empty string is never a valid status to set. Everything else (`name`, `industry`, `size`, `revenue_size`, `website`, `tags`, `notes`, `legal_name`, `address`, `tax_id`) follows the general rule — resend the current value for anything you don't intend to blank out.

```
PUT /api/v1/open/companies/42
X-API-Key: sk_live_...
Content-Type: application/json

{
  "name": "Acme Corp",
  "industry": "Retail",
  "size": "51-200",
  "revenue_size": "10M-50M",
  "website": "https://acme.example.com",
  "tags": ["vip", "renewed"],
  "notes": "Renewed for 2027",
  "status": "active"
}
```

## 6. Contacts

### `GET /api/v1/open/contacts` — List

Filters: `company_id`, `status`, `tag`, `search` (name/email), `sort` (`created_at`/`name`/`email`/`company_name`), `page`, `per_page`.

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

| Field | Required | Notes |
|---|---|---|
| `company_id` | ✅ | Must reference an existing Company. |
| `name` | ✅ | |
| `email`, `phone`, `tags` | | |
| `role_title` | | Must match an active job-title option (ask your Admin — `/admin/job-titles`). Omit if unsure. |
| `status` | | `active` or `archived`; defaults to `active`. |
| `is_primary` | | At most one Contact per Company can be primary — setting this on one automatically un-sets it on any other Contact of the same Company. |

`201 Created` on success; `422` if `company_id`/`name` is missing or `role_title` doesn't match an active option.

### `GET /api/v1/open/contacts/:id` — Get

```
GET /api/v1/open/contacts/101
X-API-Key: sk_live_...
```

### `PUT /api/v1/open/contacts/:id` — Update

Same general rule as Company Update (an omitted field is cleared, not preserved), with **two exceptions** worth calling out explicitly:

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

## 7. Error reference

Every error follows the same envelope:

```json
{ "error": { "code": "VALIDATION_ERROR", "message": "name is required", "fields": { "name": ["required"] } } }
```

`fields` is only present on `422` responses.

| Status | `code` | When |
|---|---|---|
| 401 | `UNAUTHORIZED` | Missing/invalid/revoked API key |
| 404 | `NOT_FOUND` | Company/Contact id doesn't exist |
| 422 | `VALIDATION_ERROR` | Missing required field, or `size`/`revenue_size`/`role_title` isn't an active option |
| 429 | `TOO_MANY_REQUESTS` | Over 300 requests/minute on this key |
| 500 | `INTERNAL_ERROR` | Unexpected server error — safe to retry |

## 8. Quick start (curl)

```sh
API_KEY="sk_live_..."
BASE="https://<your-domain>/api/v1"

# Create a Company
curl -sS -X POST "$BASE/open/companies" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Acme Corp","industry":"Retail"}'

# Create a Contact under it
curl -sS -X POST "$BASE/open/contacts" \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"company_id":42,"name":"Jane Doe","email":"jane@acme.example.com"}'

# Look one up
curl -sS "$BASE/open/companies/42" -H "X-API-Key: $API_KEY"
```

## 9. FAQ

**Can I delete a Company/Contact through this API?** No — Open API scope is create/read/update only. Ask an Admin to do it through the staff app.

**Can my key see other resources (Deals, Leads, …)?** No — only `/open/companies` and `/open/contacts` accept `X-API-Key`; every other route still requires the staff Bearer-JWT login.

**What happens if the staff user my key acts as gets deactivated?** The key stops working immediately (within the 30s cache window) — reactivate that user or point the key at a different `owner_user_id` (issue a new key; a key's owner can't be changed after creation).

**Is there a sandbox/test key?** Not currently — every key acts against the same live data as the staff app it's paired with.

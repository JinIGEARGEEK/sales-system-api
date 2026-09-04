# Changelog

Notable changes to this API, newest first. Dates are merge dates on `main`. See `biz_spec/api-system-spec.md` for the current contract these changes feed into.

Entries before this file existed are reconstructed from git/PR history — going forward, add an entry here in the same PR that ships the change.

## 2026-09-04 — Dormant-company / upsell-targeting

Added `Company.last_activity_at` (computed from `MAX(activities.created_at)` for company-scoped Activities only, not rolled up from Deals/Contacts — no migration/backfill needed), returned on `GET /companies` and `GET /companies/:id`, plus two new `GET /companies` filters: `stale_days` and `has_won_deal`. Implemented the Dashboard's previously-stubbed `upsell_opportunities` (`GET /dashboard/summary`): active Companies stale ≥60 days, bucketed into 3 tiers (60/90/120-day boundaries) capped at 10 companies each. Added `"company"` as a `NotificationRule.entity_type` (`checkCompanyDormantRule`), firing once per stale tier crossed, recipient resolved via the Company's most-recent Deal's owner; `GET /notification-log` gained a matching `company` branch (`company_id`/`company_name`, scoped the same way). Spec: §4, §9, §8.8.

## 2026-09-03 — Deal required-field validation dedup

No behavior change: `DealHandler.Create`/`Update` each duplicated the same `company_id`/`contact_id`/`title`-required check verbatim; extracted into `validateDealRequiredFields`, mirroring the existing `validateDealValueAndDate`/`validateProbabilityAndLostReason` shared-validator pattern.

## 2026-09-01 — Prospect entity (pre-Lead marketing funnel)

Added `Prospect`, a new funnel stage one step before `Lead`, and a new `Marketing` role to own it. `POST /prospects/:id/convert` mirrors `Lead.Convert`'s resolve-or-create-Company/Contact pattern one stage earlier, creating a `Lead` (back-referenced via the new `Lead.prospect_id`) and carrying over Attachments, guarded against double-conversion the same way (`converted_lead_id`). `Prospect.status` gained a fixed `ProspectStatus` enum (`New/Engaging/Nurturing/Disqualified/Converted`) — unlike `Lead`, which tracks conversion only via `converted_deal_id` and has no "converted" status value, `ProspectStatusConverted` is a real enum member, so Create/Update now explicitly reject a client-supplied `status: "Converted"` (`422`) to keep it settable only via Convert. `/prospects` is gated to Admin/Marketing/Sales Manager, with bulk-*/Trash/Restore staying Admin/Sales-Manager-only like Leads'. `Task`/`Activity`/`Attachment`'s shared `related_type` union gained `'prospect'`. Spec: §3a.

## 2026-08-25 — Auth session revocation, object storage abstraction, import batching, mailer TLS hardening

`POST /auth/logout` and deactivating a User now actually invalidate that user's existing JWT immediately (`User.token_version`, checked against the token's embedded value on every request), instead of a stateless no-op that left a token valid for its full `JWT_EXPIRY_HOURS` lifetime regardless. Introduced `utils.Storage` (`LocalStorage`/`S3Storage`/`MemoryStorage`) abstracting Quote/Contract/Attachment upload storage per `biz_spec/s3-migration-plan.md` — defaults to local disk (`STORAGE_BACKEND=local`, unchanged behavior), `STORAGE_BACKEND=s3` + `S3_*` vars switches to S3-compatible object storage. `POST /companies/import` and `/contacts/import` now preload existing-record matches in a handful of batched queries and run as one transaction instead of a SELECT-then-write per row, and cap a single import at 5,000 rows. `Company`/`Contact`/`Deal`/`Lead`/`User` deletes now stamp `deleted_by` and soft-delete atomically in one transaction (`utils.GenericSoftDelete`) rather than as two separate non-atomic writes. `internal/utils/mailer.go` now requires and verifies TLS (implicit or STARTTLS) rather than falling back to a plaintext connection. Added `X-Request-ID` correlation between access logs and error logs (`requestid` middleware). Batched the Task due-date reminder's per-task assignee/related-record lookups.

## 2026-08-24 — Lead `company_id` FK

Replaced `Lead.company_name` (free text) with `Lead.company_id`, a nullable FK to `Company` — matches how `Deal`/`Contact` already reference their Company, closes a dedupe gap on `POST /leads/:id/convert`. Existing rows backfilled by case-insensitive name match, or a new Company created when none matched. Spec: §3.

## 2026-08-23 — FlowAccount quote PDF extraction

`POST /deals/:dealId/quotes/upload` now attempts best-effort field extraction from an uploaded FlowAccount quotation PDF, pre-filling the new quote's number/scope-of-work/items/dates/totals instead of leaving it blank. Adds `Quote.extraction_status` (`ok`/`partial`/`failed`) and `extraction_warnings`. Spec: §7.4.

## 2026-08-23 — Quote builder rebuild

Rebuilt `Quote`/`QuoteItem` with document numbering, scope of work, reference number, issue/credit-day fields, price type, VAT/WHT, per-item and whole-quote discounts, and separate customer-facing vs. internal notes. Spec: §7.4.

## 2026-08-22 — Contract-signed-before-Won gate, source-deal-id validation fixes, Task priority + CustomerProduct fields

`FR-CRM-045`: a Deal can't move to `Won` unless it has a signed Contract. Fixed a validation gap on `CustomerProduct.source_deal_id`. Added priority to Task and additional fields to CustomerProduct.

## 2026-08-21 — Admin-configurable option lists, lead scoring, notifications, reports

Replaced hardcoded/free-text `Company.industry`/`size`/`revenue_size`, `Contact.role_title`, and Product category with Admin-editable option lists (`/admin/industries`, `/admin/company-sizes`, `/admin/revenue-sizes`, `/admin/job-titles`, `/admin/product-categories`). Added `LeadScoringCriterion`-driven `Lead.score`/`classification` (`FR-CRM-006`/`007`), a `sales-cycle` report, and the `NotificationRule`/`NotificationLog` workflow-automation engine (`FR-CRM-100`–`102`). Added six new `/reports/*` endpoints plus CSV export/sort/filter support across all reports. Spec: §8.4, §8.8.

## 2026-08-20 — Sales targets, annual revenue goal, Task bulk actions

Added per-quarter `SalesTarget` overrides (`FR-CRM-092`) and `AppSettings.annual_revenue_goal` (`FR-CRM-091`) feeding the dashboard. Added `PATCH /tasks/bulk-mark-done` and `/tasks/bulk-reassign`. Spec: §7.6, §8.6, §8.7, §9.

## 2026-08-19 — RBAC gaps, upload-serving security fixes, govulncheck CVE fix, perf/security pass

Closed several RBAC enforcement gaps, fixed unauthenticated access to `/uploads`, patched a `fasthttp` CVE flagged by `govulncheck`, and general performance/security hardening.

## 2026-08-17 — Deal forecasting, configurable pipeline stages, Quote expiration, sales quota

Added `Deal.probability` and `Quote.EffectiveStatus`-derived `expired` state, admin-configurable `PipelineStage`/`LeadSourceOption` (replacing the hardcoded `DealStage`/`LeadSource` enums), `AppSettings.quarterly_sales_target`, Quote↔Product linking, CSV export, and extended soft-delete trash/restore to Companies and Contacts.

## 2026-08-16 — Products/Projects/Contracts backend, Attachments, bulk actions

Finished the Products/Customer-Products (§8.2) and Projects (§8.3) backend, added Contract PDF export, an Attachments API, auto-convert Leads to Deals from a pipeline drag, and Deal/Lead bulk actions + trash.

## 2026-08-14/15 — Auth hardening, Railway deploy, integration tests

Company-email-only authentication (dropped username), forced password change on Admin-assigned passwords, Railway deploy support, first integration test suite, and initial `README.md`.

## Earlier

Initial backend build: Auth/Users, Leads, Companies, Contacts, Deals, Activities, Tags, Quotes (initial line-item shape), Payments, Tasks, Audit log, Dashboard aggregate — per `biz_spec/api-system-spec.md`'s original build order (§11).

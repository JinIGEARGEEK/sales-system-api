# Changelog

Notable changes to this API, newest first. Dates are merge dates on `main`. See `biz_spec/api-system-spec.md` for the current contract these changes feed into.

Entries before this file existed are reconstructed from git/PR history — going forward, add an entry here in the same PR that ships the change.

## 2026-09-10 — Terminal-stage exclusivity, dashboard date validation, Swagger scaffold

Code-review follow-up on the 2026-09-09 `PipelineStage`/`ProspectStage` and dashboard-staleness-filter changes.

**Fixed a real bug:** `PipelineStageHandler`/`ProspectStageHandler` `Create`/`Update` let an Admin flag more than one row `is_won_stage`/`is_lost_stage`/`is_disqualified_stage` at once — `DealHandler.UpdateStage`, `checkDealIdleRule`, and `checkProspectStaleRule` all resolve "the" won/lost/disqualified stage with a single `.First()` lookup, so two flagged rows left that pick undefined (whichever Postgres returned first), silently misrouting stage-transition behavior and stale-entity notifications. Both handlers now clear the flag from every other row in the same transaction as the save. New `TestPipelineStages_OnlyOneWonAndOneLostStageAtATime` (`tests/pipeline_stage_test.go`) and `TestProspectStages_OnlyOneDisqualifiedStageAtATime` (`tests/prospect_stage_test.go`). Refactored the shared "required name" validation in both handlers into a `form.validate()` method (was duplicated verbatim between Create/Update). Spec: §8.8.

`GET /dashboard/summary`'s `date_from`/`date_to` now `422` on a malformed value instead of reaching Postgres as a raw query bound — previously an invalid string failed each of Summary's ~12 concurrent aggregate queries at the driver level (their `Scan` calls discard the error), silently degrading the whole dashboard to zeroed-out figures instead of surfacing the bad input. New `TestDashboardSummary_RejectsMalformedDateParams` (`tests/dashboard_date_validation_test.go`), which also surfaced a **pre-existing, unrelated bug**: `industryBreakdown` throws `column reference "created_at" is ambiguous` when a date filter is combined with `?company_tag=` — not fixed here, flagged for follow-up. Spec: §9.

Added a dev-only (`APP_ENV=development`) Swagger UI at `GET /swagger/index.html`, generated from `@`-annotated handler doc comments (`swag init -g cmd/api/main.go -o docs --parseDependency --parseInternal`) and served from an embedded static spec rather than `swaggo/swag`'s runtime package — that package's root import pulls in the full `go/ast`-based codegen toolchain as a production dependency just to serve a JSON blob, so `docs/embed.go` embeds the generated `swagger.json` directly and `internal/routes/routes.go` serves it alongside a CDN-loaded Swagger UI page. Zero new entries in `go.mod`/`go.sum`. Currently a scaffold covering `/admin/pipeline-stages`, `/admin/prospect-stages`, and `/dashboard/summary` — the pattern extends to the rest of `internal/routes/routes.go` incrementally. `biz_spec/api-system-spec.md` remains the authoritative, hand-curated reference. Spec: §1.1.

## 2026-09-09 — Admin-configurable Prospect stages

Added `ProspectStage` (`internal/models/pipeline_config.go`), a Marketing-funnel mirror of the existing `PipelineStage` (Deal stage) pattern — `GET/POST /admin/prospect-stages`, `PATCH/DELETE /admin/prospect-stages/:id` — so Marketing's working stages (`New/Engaging/Nurturing/Disqualified`) can be renamed, reordered, added to, or deactivated from the admin panel instead of a hardcoded `ProspectStatus` enum. `"Converted"` stays outside the table — a system-set terminal status only `POST /prospects/:id/convert` sets — and Create/Update reject a client-supplied stage named `"Converted"` (`422`). `ProspectHandler.Create`/`Update` now validate `status` against active `ProspectStage` rows via a new `utils.IsActiveProspectStage` helper. Spec: §3a, §8.8.

**Follow-up same day, from code review:** added `is_disqualified_stage` (mirrors `PipelineStage.is_won_stage`/`is_lost_stage`), seeded on the default "Disqualified" row — frontend code (the "Convert to Lead" action's visibility, status badge color) resolves the actual configured stage through this flag instead of hardcoding the literal name, since it's now Admin-renamable. Also fixed a real bug this surfaced: `checkProspectStaleRule` (`internal/notifier/workflow_rules.go`, `FR-CRM-107`) excluded stale-Prospect notifications for `status = "Disqualified"` via a hardcoded string match — renaming that stage would have made genuinely-disqualified Prospects start receiving stale-Prospect notifications again. Now resolves the flagged row's actual name, falling back to the literal if none is flagged yet.

## 2026-09-09 — Open admin config-list reads, restrict Lead mutations, add SMTP status

Two RBAC fixes found while building a Production dashboard widget and fixing the Prospect Kanban board's deep-link handling. (1) `GET /admin/pipeline-stages`, `/lead-sources`, `/product-categories`, `/prospect-sources`, `/industries`, `/company-sizes`, `/revenue-sizes`, and `/job-titles` were each bundled into the same Admin-only route group as their own Create/Update/Delete, even though every one backs a plain dropdown/filter on pages with no role restriction — Marketing's own `/prospects*` pages were the worst-hit instance, since Marketing has no other resource to fall back to and got a silent 403 → hard redirect off its own primary page. `GET` is now registered directly on `authed` (open to every authenticated role) for all eight; writes stay Admin-only. (2) `PUT /leads/:id` and `POST /leads/:id/convert` had no role check at all — any authenticated role, including Marketing/Production, could mutate or convert a Lead. Now Admin/Sales Rep/Sales Manager only, matching the frontend's Mark SQL/Convert to Deal buttons' actual intent; `GET` stays open (Marketing's read-only "View Lead" access from a converted Prospect). Also added a read-only `smtp_configured` field to `GET`/`PATCH /admin/settings` (derived from `config.Config.SMTPHost` at request time), so an Admin can see from the app itself whether Task due-date email reminders can actually send. Regression-guarded: `TestRBAC_PipelineStagesLeadSourcesListOpenWritesAdminOnly`, `TestProspectSources_ListOpenWritesAdminOnly`, `TestRBAC_LeadUpdateConvertSalesPipelineOnly`, `TestSettingsGet_SMTPConfiguredReflectsConfig`. Spec: §1.7, §3, §8.6, §8.8.

## 2026-09-09 — Widen Deal reassignment history to Sales Manager

`/audit-log`'s non-Admin branch (added 2026-09-08) hard-restricted every non-Admin caller to `entity_type=deal, action=stage_changed` only, deliberately excluding `reassigned`/`bulk_reassigned` so the Deal detail page's "Owner History" card stayed Admin-only. Per `FR-CRM-025`/`M-8`, a Sales Manager is the one who actually performs Deal reassignments and needs to see that history to rebalance workload without losing accountability — Admin-only was stricter than the story called for. `AuditLogHandler.List` now splits non-Admin callers into two tiers: Sales Rep still gets `stage_changed` only; Sales Manager also gets `reassigned`/`bulk_reassigned`. `entity_type`/`actor_id` stay ignored for both — full/unrestricted browsing is still Admin-only. Spec: §8.5.

## 2026-09-08 — Deal Overview stage edits now write an audit trail

`PUT /deals/:id` (the Overview tab's full edit form) now writes a `stage_changed` audit log entry when the submitted stage differs from the deal's current one, same as the Kanban board's dedicated `PATCH /deals/:id/stage` already did. Previously a stage change made from the edit form (rather than dragging on the Kanban board) skipped the audit trail entirely — silently missing from both the Admin audit viewer and the frontend Activities pages' Deal "Pipeline History" section, which reads this same trail. New `TestDealUpdate_WritesStageChangedAuditLog` regression test. Spec: §8.5.

## 2026-09-08 — Sales Rep gains Prospects + restricted audit-log access

`/prospects` (and its `/reports/prospect-source-conversion` sibling) opened up to `Sales Rep`, matching Marketing/Sales Manager/Admin — Sales Reps now work Prospects ahead of the Lead hand-off the same way they already work Leads/Deals. `/audit-log` also opened up from Admin-only to Admin/Sales Rep/Sales Manager, but `AuditLogHandler.List` hard-restricts what a non-Admin caller actually gets back to Deal stage-change history only (`entity_type=deal`, `action=stage_changed`), ignoring any `entity_type`/`actor_id` they pass — this lets the Activities pages surface a Deal's pipeline history as read-only context without granting the Admin audit viewer's full reach into other entity types or Deal actions like `reassigned`. Spec: §1.7, §3a, §8.5.

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

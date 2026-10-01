# Changelog

Notable changes to this API, newest first. Dates are merge dates on `main`. See `biz_spec/api-system-spec.md` for the current contract these changes feed into.

Entries before this file existed are reconstructed from git/PR history — going forward, add an entry here in the same PR that ships the change.

## Unreleased — Review follow-ups

**CSV exports past 500 rows.**
- **Fixed:** the companies, contacts, deals, products and projects exports skipped and repeated rows once they had more than one 500-row page. The later pages used `FindInBatches`, which pages by id and kept the first page's offset, while the export sorts by `created_at`. Exports now page with LIMIT/OFFSET in their own order, with an `id` tie-breaker. Regression-guarded: `TestExport_PagesNeitherSkipNorRepeatRows`.

**Task owners, reassign audit, contract gate.**
- Task `assigned_to` must be an active user in a sales-pipeline role (Admin/Sales Rep/Sales Manager/Marketing), else `422` on field `assigned_to`. This applies to `POST /tasks`, `PATCH /tasks/bulk-reassign` and `POST /campaigns/:id/tasks`, and to `PATCH /tasks/:id` only when `assigned_to` changes (so a task whose owner was deactivated can still be edited). `null` is still allowed. Production can't own Tasks: the spec limits it to Projects, and moving a user to Production already hands their pending Tasks to someone else.
- Reassigning a deactivated, deleted or Production-bound user's records (`reassign_to` on `PUT /users/:id`, `DELETE /users/:id`, `PATCH /users/bulk-deactivate`) now also writes a `deal`/`reassigned` audit row for each moved Deal (before/after `assigned_to`, same as `PATCH /deals/:id/reassign`), in the same transaction and in one batched insert. The `user`/`records_reassigned` summary row is unchanged. Leads, Prospects and Tasks get no per-record row, since they have no single-record reassign audit anywhere else.
- When "require a signed contract before Won" is on, a contract now counts only if it is stored as `signed` **and** has a `signed_file_url` and `signed_date` (set by `POST /contracts/:id/upload`). Older contracts marked `signed` by hand with no file no longer count (accepted by the product owner).
- No change needed to `computeOutstandingRow`: it already uses `utils.DealReceivable`, and no duplicate receivable code is left.

**Frontend:** task assignee pickers shouldn't offer Production or inactive users, and should show the `assigned_to` `422`. Deals whose only "signed" contract has no uploaded file now get `422` `fields.stage: ["requires_signed_contract"]` when moved to Won with the gate on. Upload the signed file to fix this.

Regression-guarded: `tests/task_assignee_test.go`, `TestUserBulkDeactivate_ReassignAuditsEveryDeal` and new checks in `TestUserDeactivate_ReassignTo`, `TestUpdateStage_LegacySignedContractWithoutFileDoesNotSatisfyGate`.

**Deal value follows the Accepted quote**
- New nullable `deals.value_quote_id` (FK `quotes`, `ON DELETE SET NULL`), JSON `value_quote_id`. Single-Deal responses (`GET`/`PUT /deals/:id`, also `PATCH /deals/:id/stage`, `PATCH /deals/:id/reassign`, `POST /deals/:id/restore`) add read-only `value_quote_number` (string or null). Lists don't include it.
- When a quote with priced items (subtotal > 0) becomes Accepted (created as `accepted`, or `PUT /quotes/:id` to `accepted`), the same transaction sets the Deal's `value` to the quote's pre-VAT taxable amount rounded to satang (revenue is the taxable amount, spec §7.4) and `value_quote_id` to the quote. It writes a `deal` audit row `value_synced`: before `{value, value_quote_id}`, after `{value, value_quote_id, quote_number}`. An Accepted quote with no priced items (an uploaded PDF) changes nothing.
- When that quote is rejected, `value_quote_id` is cleared and `value` stays as it is. Audit: `value_unsynced`, before `{value, value_quote_id, quote_number}`, after `{value, value_quote_id: null}`. Only drafts can be deleted, so a synced quote can't be deleted through the API. If the row is ever removed, the FK clears the link.
- While `value_quote_id` is set, `PUT /deals/:id` with a `value` that differs from the stored one (by more than `utils.MoneyEpsilon`) is `422` with `fields.value: ["synced_from_quote"]`, and the message names the quote number. Resending the stored value is fine. The check is repeated under the Deal row lock, so a quote accepted at the same moment can't be overwritten. A full-row Deal save never writes `value_quote_id` (GORM create-only field). Lead/Prospect convert is unchanged.
- Boot backfill (runs once, `deal_value_quotes_backfill`): each non-deleted Deal whose latest Accepted quote has priced items is linked **only when its value already equals that quote's rounded taxable amount** (within `utils.MoneyEpsilon`). A Deal whose value differs is left unlinked and unchanged, and is only counted in the boot log, so the boot never rewrites revenue. No audit rows are written.

**Payments export**
- New `GET /payments/export`, Admin/Sales Manager only, the same gate as the other CSV exports. It uses the same CSV helpers: formula-injection guard and no BOM. Filename `payments-YYYYMMDD.csv`. Rows run oldest `paid_at` first.
- Columns: Paid At (YYYY-MM-DD, server-local), Document Number, Deal ID, Deal, Company, Amount, WHT Amount, Total (amount + WHT), Method, Installment ID, Installment Due Date, Note, Created By.
- Filters: `date_from`/`date_to` on `paid_at` (inclusive server-local days, `422` on a bad or reversed range), `deal_id`, `company_id`, `method` (`422` when invalid).
- Leaves out soft-deleted Payments and Payments on soft-deleted Deals.

**Frontend:**
- Show the Deal value as synced (read-only, "from quote QT…" via `value_quote_number`) while `value_quote_id` is set, and handle the `synced_from_quote` 422 on `fields.value`. Refetch the Deal after accepting or rejecting a quote, since its `value` may have changed.
- Add a payments CSV download for Admin/Sales Manager that passes the filters above.

Regression-guarded: `tests/deal_value_sync_test.go`, `tests/payments_export_test.go`. Swagger regenerated.

**Merge duplicate companies and contacts.**
- New `POST /companies/:id/merge` and `POST /contacts/:id/merge`, body `{ "source_ids": [...] }` (1–20 ids). `:id` is the record that survives. Admin/Sales Manager only (`403` otherwise). `422` on `fields.source_ids` when the list is empty, over 20, contains the target, or repeats an id. `404` when the target or a source doesn't exist or is deleted, with the missing ids in the message.
- One transaction. Target and sources are locked in id order, so concurrent merges wait for each other instead of deadlocking.
- Every reference to a source moves to the target, soft-deleted rows included. For a Company: Contacts, Deals, Leads (`company_id` and `referred_by`), Prospects, Projects, Customer Products, company Activities/Attachments/Tasks, and dormant-company notification logs. For a Contact: Deals, contact Activities/Tasks, and Lead `referred_by`. Contacts can be merged across Companies; the target keeps its `company_id` and a moved Deal keeps its own Company.
- The target keeps its non-empty fields. Empty ones take the first non-empty value from the sources, in `source_ids` order, and tags are unioned (lowercased, deduped). A source value for a field that identifies the record isn't copied if it differs from the target's; it's reported in `conflicts` instead. Those fields are Company `website` (by domain), `tax_id` and `branch_code`, and Contact `email` and `phone`. A Company merge keeps at most one Primary Contact.
- Sources are soft-deleted, so they're in Trash and no longer count as duplicates for the `409 duplicate_of` check. Restoring one gives back a record with nothing attached. A Company source's derived domain is cleared, so restoring it can't hit the unique domain index.
- Audit: `merged` on the target (`before` snapshot; `after` has `source_ids`, `moved`, `filled`, `conflicts`) and `merged_into` on each source (`after.target_id`).
- Response `200 { data: { target, moved: { <table>: n, ..., total }, filled: [field], conflicts: [{ field, source_id, value }] } }`.
- The dormant-company rule now skips soft-deleted Companies. It read `companies` without the soft-delete filter, so a deleted or merged Company could still raise "Company gone quiet".

**Frontend:** the natural entry point is the `409` duplicate envelope (`error.duplicate_of`) on Contact create, plus a "Merge into…" action on the Company/Contact detail page, shown to Admin/Sales Manager only. Show a confirmation with the list of sources. Afterwards, show `conflicts` (values that were not kept) and `moved.total`, then navigate to the target. Merged sources appear in Trash. Warn that restoring one brings back an empty record.

Regression-guarded: `tests/merge_test.go`, `TestCompanyDormantRule_SkipsDeletedCompany`. Swagger regenerated.

## 2026-10-01 — Pipeline coverage and forecast

`GET /dashboard/summary` (spec §9) now counts coverage and the forecast trend by when open Deals are expected to close. Days are server-local (`calendar.ParseLocalDay`, `calendar.Today`, new `calendar.QuarterStart`).

- **`pipeline_coverage_ratio` is this quarter's pipeline over this quarter's target.** The numerator is open Deals with `expected_close_date` in the current calendar quarter (new field `quarter_pipeline_value`), including ones earlier in the quarter that are now overdue. It used to be all open pipeline, including Deals past their close date and Deals due in later quarters. Deals with no `expected_close_date` are left out.
- **New `overdue_pipeline_value` / `overdue_pipeline_count`**: open Deals whose `expected_close_date` is before today.
- **New `undated_pipeline_value` / `undated_pipeline_count`**: open Deals with no `expected_close_date` (or one that isn't a readable date), so the pipeline coverage leaves out is still shown.
- **`forecast_trend` keeps overdue Deals.** An open Deal past its close date goes into the current month's point instead of being dropped. Each point gains `overdue`: the part of its `value` from overdue Deals (`0` on every point but the first).
- **`forecast_trend` applies the dashboard filters** (`business_unit`, `business_unit_item`, `channel`, `assigned_to`, `company_tag`). It used to ignore them all.
- The new figures and `forecast_trend` don't apply the date window (`date_from`/`date_to`/`period`, which counts by `created_at`). They're defined by close date, and the window would drop an older Deal still due this quarter. Existing fields keep their names. `open_pipeline_value` is unchanged.

**Frontend:** new fields on `GET /dashboard/summary`: `quarter_pipeline_value` (coverage numerator), `overdue_pipeline_value`, `overdue_pipeline_count`, `undated_pipeline_value`, `undated_pipeline_count`, and `overdue` on each `forecast_trend` point. Coverage numbers will usually drop. Show the overdue and undated amounts next to the coverage card, and the current month's `overdue` on the forecast chart (it's part of that month's `value`, not extra).

Regression-guarded: `tests/dashboard_coverage_forecast_test.go`, `TestQuarterStart`. Swagger regenerated.

## 2026-10-01 — Quote search; `GET /quotes/:id` registered

- **New `GET /quotes`** (`search`, `page`, `per_page`) for the frontend's global search: matches quote `number`, `reference_number` or the Deal title (case-insensitive), newest first, in the `GET /deals` envelope. Rows are the Quote plus `deal_title`; quotes on soft-deleted Deals are left out. Sales-pipeline roles only (Production `403`), no per-rep scoping (same as `GET /deals`).
- **Fixed: `GET /quotes/:id` was never registered.** The spec listed it and the frontend's full-page Quote editor calls it, but every request was a `404`. It now returns the Quote (effective status) plus `deal_title`. Sales-pipeline roles only; `404` for a missing Quote or a soft-deleted Deal.

Regression-guarded: `tests/quote_search_test.go`. Swagger regenerated.

## 2026-10-01 — Review round 2

**Access.**
- Production is now `403` on every `/companies*` and `/contacts*` route (including `/companies/:companyId/products|projects` and `PATCH /customer-products/:id`) and on the top-level `/quotes/:id*`, `/payments/:id`, `/payment-installments/:id` and `/contracts/:id*` routes, including both `export-pdf` (spec §1.7). Its Projects page is unaffected: `GET /projects` already returns `company_name`. **Frontend:** hide the Projects page's "View company" action for Production.
- Marketing keeps Sales Rep access to all of the above.
- `DELETE /companies/:id` and `DELETE /contacts/:id` are Admin/Sales Manager only (`403` for Sales Rep/Marketing), like trash/restore. **Frontend:** hide the delete action for other roles.
- `DELETE /companies/:id` returns `409` while the Company has an open or Won Deal that isn't deleted.
- Single Company/Contact delete and restore write `company`/`contact` audit entries (`deleted`, `restored`).
- The Open API `/open/companies`, `/open/contacts`, `/open/leads` and `/open/prospects` routes are `403` for a key owned by a Production user, like `/open/deals`. `/open/projects` and `/open/products` are unchanged.

**Users and ownership.**
- Deactivating a user (`PUT /users/:id` with `status: "inactive"`, `PATCH /users/bulk-deactivate`), deleting one (`DELETE /users/:id`), or moving one to a role that can't own pipeline records (Production) accepts an optional `reassign_to`: an active Admin/Sales Rep/Sales Manager/Marketing user, not one of the users being removed (`422` on field `reassign_to` otherwise). Their open Deals (status `open`), Leads and Prospects (not converted or disqualified) and pending Tasks move to that user in the same transaction, with one `records_reassigned` audit row per user giving the counts. Closed records keep their owner. On `DELETE`, `reassign_to` can be a query param or a JSON body.
- Those responses now report `open_records` (`{deals, leads, prospects, tasks, total}` still owned), and `reassigned` (same counts plus `user_id`, `reassign_to`) when `reassign_to` was given, so the UI can offer a reassign. **`DELETE /users/:id` and `PATCH /users/bulk-deactivate` now return `200` with a body instead of `204`.** `PUT` adds the two fields to the user object. Bulk returns arrays: `open_records` per listed user (with `user_id`) and `reassigned` per user that had records moved. `bulk-activate` still returns `204`.
- An Admin can't change their own role or deactivate or delete themselves (`422`, on field `role`/`status`/`id`/`ids`). Nobody can demote, deactivate or delete the last active Admin (`409`). The check locks the active Admin rows, so two Admins removing each other at the same time can't both succeed.
- `PUT /users/:id` writes `role_changed` and `activated`/`deactivated` audit rows (bulk was already audited).
- Deal `assigned_to` must be an active user in a sales-pipeline role (`422` on field `assigned_to`) on `POST /deals` (and Lead Convert, which shares the check), `PATCH /deals/:id/reassign`, and the deal/lead/prospect `bulk-reassign`. `PUT /deals/:id` checks it only when it changes, so a deal whose owner was deactivated can still be edited.
- On `PUT /deals/:id` a Sales Rep or Marketing user can keep their deal or claim an unassigned one, but not unassign it or give it to someone else (`403`).
- `PUT /deals/:id` audits `value`/`company_id` changes (`updated`) and `assigned_to` changes (`reassigned`, the same shape as `PATCH /deals/:id/reassign`, so Sales Managers see them), with before/after.

**Quote and contract lifecycle.**
- Supersedes the 2026-10-01 Accepted-quote pricing lock: an Accepted or Rejected quote is now fully read-only, so any change other than an allowed status move is `409` (it was a `422` with `fields` code `accepted_locked` for pricing fields only). Nothing in the frontend read `accepted_locked`.
- Quote status moves follow a fixed table (`models.CanTransitionQuoteStatus`): draft → sent/accepted/rejected, sent → draft/accepted/rejected, accepted → rejected, rejected → nothing. A Sent quote past its validity date (shown as `expired`) can't be accepted; move it back to draft with a new `validity_date`, or duplicate it. Anything else is `409`.
- An Accepted or Rejected quote is read-only: a `PUT` that would change any field other than `status` is `409`. Resending the stored values, or sending only `{"status": "rejected"}`, is fine.
- One Accepted quote per Deal. Accepting (or creating as accepted) while another quote of the Deal is Accepted is `409` naming that quote's number; reject it first. The Deal row is locked, so two concurrent accepts can't both win. Every quote save also re-checks the stored status under a row lock, so a stale full-row `PUT` can't overwrite a concurrent status change (`409`, reload).
- `DELETE /quotes/:id` only deletes drafts (`409` otherwise).
- `POST /quotes/:id/duplicate` sets the new `revision_of_id` (the chain's root quote, FK, `ON DELETE SET NULL`) and `revision_no` (chain max + 1; `0` on an original). The original is not changed.
- Quote Create/Update return `422` with `error.fields` for: item `qty` ≤ 0, `price` < 0, `discount_percent` outside 0–100 (keys `items[i].qty` etc.), `discount_total` above the items' subtotal, `wht_rate` outside 0–100, and an `issue_date`/`validity_date` that isn't a date.
- A contract becomes `signed` through `POST /contracts/:id/upload`. Create with `status: "signed"` is `422`, and so is `PUT` unless the contract already has a signed file and `signed_date`.
- A contract whose stored status is `signed` is locked: any `status`/`quote_id`/`end_date` change is `409`, and a second signed upload is `409`. Further files go on as Attachments. A signed contract past its `end_date` stays locked.
- Quote and contract status changes write a `status_changed` audit entry (`entity_type` `quote`/`contract`, before/after `status`).

Regression-guarded: `tests/quote_lifecycle_test.go`, `tests/contract_lock_test.go`.

**Duplicates, convert and import.**
- `POST /leads`, `POST /prospects` and `POST /contacts` return `409` when a non-deleted record of the same kind has the same email (case-insensitive) or phone (digits only, `+66` read as a leading `0`; `utils.NormalizePhone`). The body is the usual `CONFLICT` envelope plus `error.fields` (`{"email": ["duplicate"]}` and/or `{"phone": ["duplicate"]}`) and `error.duplicate_of` (matching ids, at most 10). **The frontend should show the match and offer to resend with `?allow_duplicate=true`**, which skips the check. Contacts are checked across all Companies. Updates are not checked.
- Lead and Prospect Create/Update: `assigned_to` must be an active user in a sales-pipeline role (`422` on `assigned_to`, `validateAssignee`). Update only checks an owner that changed, so resending a since-deactivated owner still saves.
- Both convert endpoints reuse a Contact already in the target Company with the same email (case-insensitive) instead of always creating one. A Company they create is never nameless: new optional body field `company_name`, else the source record's soft-deleted Company's name, else the Lead/Prospect's name. With none of those it's a `422` asking for `company_id` or `company_name`.
- `POST /contacts/import` matches on lower(email) **within the row's Company** only, so a Contact is never moved to another Company (a same-email row for another Company creates a new Contact). An empty phone/role_title cell keeps the stored value. A `company_id` that names no Company is a `422` before anything is written.
- Both imports save each row under a savepoint. A row Postgres rejected used to abort the transaction, so every later row failed and the import was a `500`. Now that row is reported in `errors` and skipped. A row with a NUL byte is skipped while parsing, since it failed the batched lookup query for the whole file.

Regression-guarded: `tests/duplicate_detection_test.go`, new cases in `tests/import_test.go` and `tests/lead_company_test.go`, `internal/utils/contact_match_test.go`.

**Won deals and payments.**
- A Won Deal with money attached (a non-deleted Payment, any Payment Installment, or a Contract stored as `signed`) is protected. `DELETE /deals/:id` and any move out of Won (`PUT /deals/:id` or `PATCH /deals/:id/stage`, to an open stage or Lost) are `409` with `error.code` `WON_DEAL_PROTECTED` for anyone but Admin/Sales Manager. A manager must pass `?reason=` (query string, max 500 chars). Without it the answer is `409` `REASON_REQUIRED`, so the frontend can ask for a reason and retry.
- **`PATCH /deals/bulk-archive` now returns `200 { archived: [...], skipped: [{ id, reason: "won_deal_with_money" }] }`** (was `204`). Protected Deals are skipped, not archived, and the rest of the batch still goes through. Lead/Prospect bulk archive is unchanged (`204`).
- Audit log: a Deal `DELETE` writes `deleted` (with the manager's `reason` when forced) and Restore writes `restored`. A forced un-win writes `won_reversed` with the reason, on top of the usual `stage_changed`.
- `PATCH /deals/:id/stage` into a Lost stage requires `lost_reason` (`422`, `fields.lost_reason`), like `PUT`. A Deal that is already Lost with a stored reason can still be repositioned without one.
- Payments are soft-deleted now (`AuditedModel`; AutoMigrate adds `deleted_at`/`created_by`/`updated_by`/`deleted_by`). A deleted Payment drops out of the Payments list and totals, installment statuses, the outstanding-balance report and the payment-installment rule, all through GORM's default scope.
- Payment Create/Update/Delete write `payment` audit entries (`created`/`updated`/`deleted`, before/after). Payment-installment Update/Delete write `payment_installment` entries.
- New payment checks:
  - Create on a Lost Deal is `422` (`fields.deal_id`).
  - `paid_at` later than today (server-local) is `422` (`fields.paid_at`).
  - A non-empty `document_number` already used by another non-deleted Payment, on any Deal, is `409`. Update only checks this when the number changes.
  - If cash + WHT would pass the Deal's receivable (the Outstanding Balance rule: latest Accepted Quote incl. VAT when it has priced items, else Deal value) by more than `utils.MoneyEpsilon`, the request is `422` `fields.amount: ["exceeds_receivable"]` unless the body sends `allow_overpayment: true`. Update only checks this when the payment's own cash + WHT goes up.
  - Saves lock the Deal row, so concurrent payments are checked one at a time.
- Deleting an installment also unlinks deleted Payments that pointed at it.
- `utils.ErrBulkSkip` lets a `BulkUpdate` apply leave one row alone without failing the batch.

Regression-guarded: `tests/won_deal_protection_test.go`, `tests/payment_guards_test.go`. Swagger annotations updated; regenerate `docs/` after merging.

## 2026-10-01 — Quote money fixes: tax-inclusive VAT, satang rounding, Accepted lock, schedule cap

- **Tax-inclusive quotes no longer charge VAT twice.** With `price_type: "incl_tax"` and VAT on, `ComputeQuoteTotals` backs VAT out of the prices (taxable = net × 100/107, VAT = net − taxable) instead of adding 7% on top. Affects the quote PDF, the Outstanding Balance receivable and expiring-soon `total_value`. `excl_tax` and VAT-off quotes are unchanged. WHT stays on the pre-VAT amount.
- **Totals round to satang at every step** (line, subtotal, net, taxable, VAT, WHT; grand total from the rounded parts), half up like the frontend, so the PDF's printed lines add up exactly. A receivable can move by a satang.
- **Accepted quotes' pricing is locked.** `PUT /quotes/:id` on a stored-Accepted quote returns `422` (`accepted_locked`) for a change to items, price type, VAT/WHT or discount. Same values resent, status changes and text fields still work.
- **Generated payment schedules can't exceed the receivable.** `POST /deals/:dealId/payment-installments/bulk` returns `422` (`exceeds_receivable`) when existing + new installments would total more than the Deal's receivable (skipped when it's 0).
- **`Deal.won_at`** (new nullable, indexed column): when the Deal became Won. A `BeforeSave` hook on `Deal` sets it on the way into status `won` (Kanban stage move, `PUT`, create, Lead convert), keeps it on later re-saves, and clears it when the Deal reopens or is lost. On boot, `database.BackfillDealWonAt` fills it for won Deals that don't have one (from `stage_entered_at`, else `updated_at`) and clears it on Deals that aren't won. It only touches rows that are out of step, so it runs again on every boot.
- **`GET /dashboard/summary`**: `won_value`, `win_rate`, the Won/Lost bars in `stage_breakdown`, and the won/lost numbers in `industry_breakdown` and `team_performance` now count Deals **won or lost inside the window**: won by `won_at`, lost by `stage_entered_at`. Before, they counted Deals created in the window. `revenue_trend` and `annual_revenue_trend` now bucket by `won_at`. Open-pipeline figures still go by `created_at`. `avg_deal_size` is now the average of Deals won in the window (FR-CRM-057), not of every Deal. New fields: `deals_count` (Deals matching the filters) and `total_deals_count`.
- **`GET /reports/win-loss-reasons`** (and its CSV export): the date range now filters on when the Deal closed, not on `created_at`.
- **`PipelineStage.default_probability`** (read-only, on every `/admin/pipeline-stages` response): the probability a Deal gets in that stage when none is sent (`utils.DefaultProbabilityFor`). The frontend uses this in place of its own table.
- **`Contract.effective_status`** (read-only, on every contract response): `expired` once a `signed` contract's `end_date` has passed, using the server-local day. `status` stays `signed`, so the signed-contract Won gate still counts the contract.

## 2026-09-28 — Review pass: sessions, access, deal states, report dates, deploy hardening

Fixes from a full review of auth, handlers, reports and infrastructure.

**Sessions and access.**
- The caller's role now comes from the DB on every request (cached with `is_active`/`token_version`), not the login token's claim. A role change, Admin password reset, deactivation (single or bulk) or delete bumps `token_version`, so the user's existing tokens stop working immediately.
- `POST /auth/change-password` also revokes the caller's older tokens and returns a fresh one in `data.access_token`. **The frontend must store it**, or the user is signed out after changing their password.
- `POST`/`PUT /users` return `422` for an empty or unknown `role`. Creating a user with `status: "inactive"` now really stores them inactive.
- Login checks the password before reporting "Account is inactive".
- `GET /attachments` requires `related_type` + `related_id` (`422`) and checks access to the parent record. Deal, quote and prospect attachments are `403` for Production, and a missing parent is `404`. `POST /attachments` checks the parent exists and the caller may write to it (`404`/`403`) before storing a file. `external_url` must be http(s). `/uploads/:key` now also requires a changed password.
- The login rate limit keys on the real client: `X-Forwarded-For` is only read from `TRUSTED_PROXIES` peers, right to left, so a client-sent value can't pick a fresh bucket.

**Explicit `false` on Create.** Products, quote templates, option-list items, pipeline/prospect stages and lead scoring criteria now keep `is_active`/`vat_enabled: false` (`utils.CreateKeepingFalse`), like users above.

**Deals, Leads, Prospects.**
- Moving a Won/Lost deal to an open stage (Kanban `PATCH /deals/:id/stage` or `PUT`) sets status `open` and clears `lost_reason`. A `PUT` that omits `stage`/`status` keeps the stored values instead of blanking them.
- `POST /leads/:id/convert` runs Deal Create's checks: value/date, stage/channel/business unit, `lost_reason` for Lost (new optional `deal.lost_reason`), the signed-contract gate for Won, `CanWrite` on `assigned_to`. The status follows the stage.
- Both convert endpoints lock the row, so a concurrent second convert is `409` instead of a duplicate Deal/Company/Contact. A missing explicit `company_id`/`contact_id` is `404` (was `500`); a contact from another Company is `422`.
- Lead `status` must be New/Contacted/Qualified/Disqualified (`422`; over 16 chars was a `500`). A Lead or Prospect `PUT` without `status` keeps it.
- Searched Lead/Prospect lists keep their sort order (they had no `ORDER BY` with the Company join).

**Reports and dates.**
- `date_from`/`date_to` on the lead/prospect source-conversion, top-referrers, win/loss, sales-cycle, dashboard summary, lead/prospect summaries and `/audit-log` (and their CSV exports) are inclusive server-local days (`utils.ParseDateRange`). They were UTC midnight, which dropped 00:00–07:00 Bangkok and most of the last day. A malformed or reversed range is `422`, with the same message everywhere (`/pipeline/overview` included, which had its own wording). `from`/`to` are accepted as aliases wherever `date_from`/`date_to` are.
- A Sent quote stays `sent` through its whole validity date and shows `expired` from the next local day. The same applies to expiring-soon and the `quote` notification rule. Expiring-soon `total_value` is now the grand total (discounts, VAT, WHT).
- Dashboard trends no longer repeat or skip a month on the 29th–31st, and bucket months at Bangkok midnight.
- `sort=company_name` with filters on `/deals` and `/contacts` returned `500` ("ambiguous column"). Filter columns are now table-qualified, with an id tie-breaker.
- Top referrers, customers-by-product-status, campaign progress and the `has_won_deal` filter ignore soft-deleted rows.
- **Sales cycle never worked:** the audit JSON was never scanned, so `by_stage` was empty and `avg_sales_cycle_days` 0 (also on the dashboard). It now returns real figures and narrows audit rows in SQL.

**Deploy and background jobs.**
- `internal/server.New` builds the app for both `main` and the tests (error handler, recover, body limit), so the test suite exercises the production stack.
- The body limit is 11 MB (the 10 MB upload limit was unreachable behind Fiber's default 4 MB), with 2-minute read/write/idle timeouts.
- The Dockerfile creates a writable `/app/uploads`; local-storage uploads failed with "permission denied" as the non-root user.
- Graceful shutdown on SIGTERM (20s drain, jobs stopped via context). Every job tick recovers from panics. `railway.toml`: restart `ALWAYS`, `drainingSeconds = 30`.
- `/health` pings the DB (`503` when unreachable).
- The DB pool is bounded (`DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME`, `DB_CONN_MAX_IDLE_TIME`).
- Boot migrations, backfills and seeds run under a Postgres advisory lock, so replicas booting together don't race. The tag-lowercasing and `previous_stage` backfills no longer rewrite or scan every row on each boot.
- Task due reminders claim each task before emailing (at most once across instances). A deleted, inactive or email-less assignee is stamped rather than retried every 15 minutes.
- Forecast snapshots skip the day on a query error (instead of writing zeros), use the local date, and retry hourly.
- The seeded Admin uses `ADMIN_INITIAL_PASSWORD` if set. Otherwise the generated password is printed once to stderr outside development.
- CI builds the Docker image, boots it against Postgres, and checks `/health` and a clean stop.
- `TEST_DB_NAME` overrides the test database.
- A negative or malformed `JWT_EXPIRY_HOURS` now falls back to 720 with a log line (a negative value was accepted before).

**New env vars:** `TRUSTED_PROXIES` (on Railway defaults to the private ranges; elsewhere trusts nothing), `ADMIN_INITIAL_PASSWORD`, `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME`, `DB_CONN_MAX_IDLE_TIME`.

**Cleanup.**
- Calendar-day helpers live in a new leaf package `internal/calendar` (`Day`, `LocalDay`, `Today`, `DaysUntil`, `LocalDaysBetween`, `Parse`, `ParseLocalDay`, `ParseLocalMidnight`), so `models` uses them instead of its own copy. `utils` keeps `DateRange`/`ParseDateRange`, which now returns a `*utils.DateRangeError`. `reportError` is the only place that writes the date-range 422.
- `Quote.ExpiresWithin` is shared by the expiring-soon report and the quote rule. `models.ParseValidityDate` is removed.
- Deal status from stage is decided in one place: one `utils.LookupStageFlags` query (replacing `IsWonStage`/`IsLostStage`) and `resolveDealStatus`, used by Create, Update, the Kanban move and Lead Convert. Deal Create and Lead Convert share `validateNewDealForm`.
- Leads and Prospects share `applyLeadLikeSort`. One `bumpTokenVersion` (`UPDATE … RETURNING`) handles every session revocation.
- `models.SalesPipelineRoles`, `IsValidRole`, `IsValidLeadStatus` and `utils.MinPasswordLength` replace hand-copied lists and constants.
- Attachment access is table-driven, and a Quote's owner is resolved in one query.
- `TRUSTED_PROXIES` is parsed once in `main` and passed to `server.New`/`routes.Setup`. One `database.AdvisoryLock` serves both boot and the tests. Pool defaults are `config.Default*` constants.
- Tests pin `time.Local` once per package in `TestMain`, and share `listIDs`.

Regression-guarded: `tests/{session_invalidation,attachment_access,review_pass_deals,reports_date_range,quote_validity,dashboard_local_month,company_name_sort,reports_soft_delete,boot_backfill,server_config}_test.go`, `internal/notifier/hardening_test.go`, `internal/clientip`, `internal/calendar`, `internal/utils/date_range_test.go`, `internal/models/quote_expiry_test.go`, `internal/handlers/dashboard_trend_internal_test.go`. Swagger regenerated.

## 2026-09-27 — Post-release fixes: receivables, installment alerts, dates, PDFs

Fixes found reviewing the release below, plus the cleanup around them.

**Fixed.**
- Outstanding balance: an Accepted quote with no priced items (an uploaded PDF whose extraction failed) made the receivable 0, so an unpaid Won Deal vanished from the report. Such a quote now falls back to the Deal value (`receivable_source: "deal_value"`).
- Installments: cash + WHT summing a hair under the amount left an installment partial/overdue (and fired a High "Overdue payment" Task). Paid now allows 0.005 baht (`utils.MoneyEpsilon`, also the report's threshold).
- Installments are overdue from the calendar day after `due_date`, not on the due date itself — matching `days_overdue`/`aging_bucket`.
- `payment_installment` rule: the due-soon firing used up the only dedupe key, so the overdue alert never came. It now fires once per state (`context` `due_soon`/`overdue`); a one-time migration (`BackfillInstallmentAlertContexts`) re-keys existing log rows so nothing re-alerts on deploy.
- Rule Tasks/emails went to deactivated owners. An inactive owner now counts as no owner (logged only if someone else, e.g. managers, was alerted).
- Notification-rule Create wrote explicit `false` flags in a second, untransacted statement, so a failure could leave the rule active after a 500. Now one transaction (`utils.CreateKeepingFalse`, also used for a quote's `vat_enabled`).
- Bare `YYYY-MM-DD` for `updated_since`/`due_from`/`due_before` was UTC midnight (07:00 Bangkok); now server-local midnight.
- Customer-product `end_date`: an unparseable value is a `422` (was silently dropped with a `200`), and Create applies it (was ignored). RFC 3339 is stored as sent; a bare date is local midnight.
- Quote/Contract PDFs wrap long party lines (e.g. a full Thai address) instead of clipping them.

**Behaviour changes.** Alert titles ("Deal idle N days") count local calendar days. Renewal/contract-expiry checkers filter their window in SQL. Every rule checker takes the tick's `now`.

**Cleanup.** One set of calendar-day helpers (`utils.DaysUntil`/`LocalDaysBetween`; `DaysOverdue` and `notifier.daysSince` removed), one body-key helper (`bodyKeys`/`bodyHas`), `parseOptionalCalendarDate`, pointer helpers in `utils/ptr.go`; removed `recordNotified`, `fireRule`'s unused result and `utils.ComputeInstallmentStatuses`. Swagger now documents `/admin/notification-rules`.

Regression-guarded: `internal/handlers/{receivables,filters}_internal_test.go`, `internal/utils/{payment_schedule,pdf}_test.go`, `internal/notifier/{payment_installment_rule,rule_tasks}_test.go`, `tests/{create_keeping_false,installment_alert_context_migration}_test.go`, `TestCustomerProduct_EndDate`.

## 2026-09-27 — In-app alerts without SMTP, payment tax fields, receivables, renewals, Thai PDFs

The company runs without SMTP, so alerts now arrive in-app, and payments carry what Thai accounting needs.

**Email-free operation.** With `SMTP_HOST` unset the server logs "email is disabled" once at startup (`utils.LogMailStatus`) and `SendMail` no-ops silently — no per-recipient log line every 15 minutes, no errors. Tickers still do their in-app work. `POST /admin/weekly-digest/test` still `422`s without SMTP, on purpose.

**Rules create Tasks.** A `NotificationRule` firing also creates a Task for the owner (new `create_task`, default `true`, existing rules migrated to `true`): due today, `high` for an overdue installment else `medium`, related to the Deal (deal/quote/contract/installment/contract_expiry), Company (company, customer_product_renewal) or Prospect. Deduped by the `NotificationLog` insert (`ON CONFLICT DO NOTHING`, same transaction as the Task). Rule Tasks are pre-stamped `notified_at`. Rule Create now keeps an explicit `is_active`/`create_task` `false`.

**Renewals.** `CustomerProduct` gains `renewal_date` (date), `billing_cycle` (`monthly|yearly|one_time`), `price`; `Contract` gains `end_date` (date). New rule types `customer_product_renewal` and `contract_expiry` fire from `threshold_days` before the date to 30 days after, once per date value; one of each is seeded active (30 days) at boot if none of that type exists.

**Payments.** `wht_amount`, `wht_certificate_received` (50 ทวิ), `document_number` (FlowAccount receipt/tax invoice no.), `installment_id` (same Deal). New `PUT /payments/:id` (partial merge). List adds `total_wht`, `total_settled`. Installment status: linked payments settle their own installment first, the rest waterfalls as before; cash + WHT both count.

**Outstanding balance.** Receivable = latest Accepted Quote's taxable + VAT (before WHT), else `deals.value`; outstanding = receivable − (paid + WHT). New fields `receivable_amount`, `receivable_source`, `wht_amount`, `oldest_overdue_due_date`, `days_overdue`, `aging_bucket`; CSV appends matching columns.

**Source performance.** `GET /reports/source-performance` (+ `/export`): leads → qualified → Won Deals/value → `win_rate` per lead source, attributed through the Lead rather than `deals.channel`; lead-less Won Deals in `direct_*`.

**Duplicate quote.** `POST /quotes/:id/duplicate` → new Draft with a fresh number, `issue_date` today, validity recomputed from the original's term. Fixed: `vat_enabled: false` on quote create/upload was stored as `true`.

**Thai PDFs.** Quote/Contract PDFs embed Sarabun (SIL OFL, `internal/utils/fonts/`), so Thai renders; branch prints as สำนักงานใหญ่ / สาขาที่ 00001.

Regression-guarded: `tests/in_app_alerts_payment_tax_test.go`, `internal/notifier/rule_tasks_test.go`, `internal/handlers/receivables_internal_test.go`, `internal/utils/{mailer,payment_schedule,pdf}_test.go`. Swagger regenerated.

## 2026-09-27 — Company tax ID matching: normalization, tax ID + branch dedupe, `updated_since`

Follow-ups so the accounting sync can match Companies reliably.

**Tax IDs are normalized.** Create/Update store `tax_id` with every space and dash removed (`utils.NormalizeTaxID`, Unicode-aware, so a pasted non-breaking space or en dash counts), and a value with nothing left becomes `null`. `?tax_id=` is normalized the same way, so `0-1055-55555-55-5` finds `0105555555555`; one that normalizes to nothing (`-`, a space) matches no Company rather than silently dropping the filter and returning them all. Existing rows are rewritten on boot by `database.NormalizeCompanyTaxIDs`. It includes soft-deleted rows, uses `UpdateColumn` so `updated_at` doesn't move, and only loads rows that aren't plain digits already, so re-running it is cheap.

**Tax ID + branch dedupe.** Create/Update return `409 CONFLICT` when another Company has the same `tax_id` + `branch_code`. A `null` branch only matches another `null`, and a Company without a `tax_id` is never checked. On Update it runs only when the pair changes, so rows that already shared a pair can still be edited. It's an app-level check with no unique index, because existing data may hold duplicates the index couldn't be built over; integrations still look up before creating.

**Validation writes last.** `validateCompanyForm` now runs every check, the two duplicate checks included, before `industry` auto-registration. Before this, a request rejected with `409` for a duplicate website had already added its new industry option. It also takes the current Company, so the Update-only rule "omitted `branch_code`/`postal_code` keep their value" lives next to the other checks instead of in the handler.

**Incremental sync.** `GET /companies` (so also `/open/companies` and `/companies/export`) gains `updated_since` (inclusive; RFC 3339 or `YYYY-MM-DD`; `422` if unparseable), and `sort` accepts `updated_at`.

**Search covers tax IDs.** `?search=` also matches `tax_id`, with the term's spaces/dashes dropped. A term with nothing left after that ("-") skips the tax ID column rather than matching every row.

**PDFs.** Quote and Contract PDFs append `postal_code` to the address line and print the branch with the tax ID ("Tax ID: 0105555555555 (สำนักงานใหญ่)", "(สาขาที่ 00001)", the wording on Thai tax documents). Both now share `utils.CompanyPartyLines` instead of duplicating the block, and `handlers.derefStr` is replaced by the shared `utils.DerefString`.

**Swagger.** `docs/swagger.json`/`.yaml` are regenerated, which also picks up earlier annotation changes never regenerated. Company Create/Update now document their `409`/`422` responses (the old `400` note wrongly listed a missing name, which is a `422`). `cmd/api/main.go` now defines the `ApiKeyAuth` (`X-API-Key`) scheme the Open API routes were already tagged with. `docs/embed.go`'s regen steps note to delete the `docs/docs.go` swag also writes.

Regression-guarded: `tests/company_tax_id_test.go`, `TestNormalizeTaxID` and `TestCompanyPartyLines` (`internal/utils`). Spec: `api-system-spec.md` §4, §7.4, §8.1. Guide: `docs/OPEN_API_GUIDE.md` intro, §6, §12b, §13.

## 2026-09-27 — Company `branch_code`/`postal_code`, exact `tax_id` filter

Requested by the IGG Finance accounting integration, which identifies a customer by its 13-digit tax ID plus branch number, and needs the buyer's branch on full tax invoices.

**New Company fields** `branch_code` and `postal_code` (string | null, both returned on List/Get and accepted on Create/Update, staff and Open API). Each must be exactly 5 digits when set (`422` otherwise). Values are trimmed, and blank is stored as `null`.

**Update keeps them when omitted.** `PUT /companies/:id` is otherwise a full replace, but the staff Company form and earlier integrations don't send these fields yet, so a body without the key leaves the saved value alone. Explicit `null` or `""` clears it. This is the same rule `stale_days` uses on the stage config resources.

**New list filters** `tax_id` and `branch_code` on `GET /companies` (and so `/open/companies` and `/companies/export`): exact match, surrounding spaces ignored. `companies.tax_id` is now indexed. There is still no dedupe on `tax_id`, since one tax ID can have several branches. Integrations look up before creating.

The Companies CSV export gains `Branch Code` and `Postal Code` columns after `Tax ID`.

The 5-digit checks run before `industry` auto-registration, so a request rejected for a bad code doesn't leave a new industry option behind.

Regression-guarded: `TestOpenAPI_CompanyTaxIDBranchCodeFilter`, `TestOpenAPI_CompanyBranchPostalCodes` (`tests/open_api_test.go`), `TestExport_CompaniesIncludesBranchAndPostalCode` (`tests/export_test.go`). Spec: `api-system-spec.md` §4. Guide: `docs/OPEN_API_GUIDE.md` §6, §12b, §13.

## 2026-09-25 — Server-side paging filters for Tasks/Activities; audit-log `action` filter

The frontend's Tasks and Activities list pages used to load one capped page (`per_page=1000`, which `utils.Pagination` silently reset to 20, and `per_page=200`) and filter/page it client-side. They now page server-side, so the list endpoints gained the filters those pages need. Existing callers are unaffected: every addition is opt-in.

**`GET /tasks`** (`applyTaskFilters`): `related_type` alone, `assigned_to=unassigned`, `search` (title/description or the linked record's display name), `business_unit` (via the linked Deal/Prospect/Lead), and `due_from` (inclusive) / `due_before` (exclusive), each RFC 3339 or `YYYY-MM-DD` (422 if unparseable). `sort` also allows `title`.

**`GET /activities`**: `related_type` alone (a lone `related_id` is still a 400), `search` (subject/notes or the linked record's name), and `include_stage_changes=true`, which `UNION ALL`s Deal stage-change audit rows into the same filtered, counted, sorted and paged list (`internal/handlers/activity_feed.go`). Stage rows carry `kind`/`type` `stage_change`, `related_type` `deal`, `from_stage`/`to_stage`. The flag is ignored for roles outside the `/audit-log` gate.

**Search is literal everywhere.** `%`, `_` and `\` in any list endpoint's `search` (these new ones and the pre-existing Companies/Contacts/Deals/Products/Leads/Prospects/Tags/Users/Trash/Overview Pipeline searches) are escaped (`utils.LikePattern`, `ILIKE ? ESCAPE '\'`) instead of acting as LIKE wildcards.

**`GET /audit-log`** now honors `action` for every role (narrowing only). It was ignored, so `action=stage_changed` also returned other Deal audit rows, which the frontend rendered as an empty "Stage set:" row.

Regression-guarded: `tests/task_activity_paging_test.go`. Spec: `api-system-spec.md` §7.2, §7.6, and the audit-log row.

## 2026-09-25 — Weekly Overview Pipeline email, safe stage renames

**Weekly digest (FR-CRM-124).** Every Monday from 08:00 server-local time, active Admins and Sales Managers get last week's Overview Pipeline by email: the summary with cohort conversion, stale Deals (longest-waiting first), Deals that slipped back, Deals lost with their reason, and a link to the board. It's built by `internal/overview`, the same code as `GET /pipeline/overview`, so the email and the page agree. The hourly check claims the week with one conditional update before sending, so two API instances can't both send it, and releases the claim only when every send failed. An Admin can turn it off (`weekly_digest_enabled` on `PATCH /admin/settings`, optional; a settings save never touches `last_weekly_digest_at`), preview it (`GET /admin/weekly-digest/preview`) and send themselves a test (`POST /admin/weekly-digest/test`). Needs SMTP; the link needs `APP_URL`.

**Time zone.** The Docker image sets `TZ=Asia/Bangkok` and the binary embeds the tz database. The alpine image ships no zoneinfo, so `time.Local` was UTC: the digest would have gone out at 15:00 Bangkok time, and the Overview Pipeline's "today" started at 07:00.

**Stage renames carry records along.** Renaming a pipeline or Prospect stage repoints every Deal/Prospect storing it by name (`stage`/`status` and `previous_stage`, soft-deleted rows included) in the same transaction. New Deals and Prospects start in the first open stage rather than the literal `Lead`/`New`, Lead→Deal conversion falls back to it when `Qualified` is gone and takes that stage's configured probability, and the Marketing dashboard finds the Disqualified stage by its flag. Stage names are trimmed and limited to the column they're stored in (64 characters for pipeline stages, 16 for Prospect stages); a too-long or already-used name is a `422`, not a `500`. Known limitation: the time-in-stage report groups audit history by the name at the time of each move, so a renamed stage's history shows under its old name.

Regression-guarded: `tests/weekly_digest_test.go`, `tests/stage_rules_test.go`, `internal/digest/weekly_internal_test.go`. Spec: `api-system-spec.md` (settings, weekly digest, pipeline/Prospect stages, `/prospects`, `/pipeline/overview`).

## 2026-09-25 — Open API: read a Deal's payment schedule

New read-only `GET /open/deals/:dealId/payment-installments` (`X-API-Key`). It returns the Deal's planned installments with their derived `covered`/`status`, the same response as the staff route (§7.5a). An integration can now get a customer's payment milestones by listing their Projects and following each `deal_id`.

It reuses `PaymentInstallmentHandler.List`, so the staff route's rules carry over: Production-owned keys get `403`, and a key owned by a Sales Rep or Marketing user only reads schedules on that user's own or unassigned Deals. Admin/Sales Manager keys read every Deal. That is stricter than Open API Prospect/Lead reads, which aren't scoped by owner. No other Deal route is exposed, and there are no installment writes.

Regression-guarded: `TestOpenAPI_DealPaymentInstallmentsList` in `tests/open_api_test.go`. Spec: `api-system-spec.md` §7.5a, §8.9. Guide: `docs/OPEN_API_GUIDE.md` §11a.

## 2026-09-24 — Kanban card position: fill the gaps left by the first version

Follow-ups to 2026-09-22's persisted card positioning (`Deal`/`Lead`/`Prospect.position`):

**Every lane change now sets a position.** Lead→Deal conversion (the new Deal, and the Lead moving to `Qualified`), Prospect→Lead conversion (the new Lead, and the Prospect moving to `Converted`), and a stage/status change through the full `PUT` edit forms used to keep the old value, or 0 for new rows. That put converted cards at the top of their lane, and they jumped to the bottom on the next boot's backfill. They now append to the end of the destination lane, the same as a dropdown-move.

**Backfill runs once.** `database.BackfillCardPositions` used to re-run on every boot against every row at `position = 0`. But 0 is also a valid drag result, so a card dropped there moved on every deploy. It now runs a single time, recorded in a new `data_migrations` table (`database.runOnce`). That run appends zero-position rows after each lane's existing cards instead of numbering from 1, so it repairs rows the conversion bug left at 0 without colliding with real positions.

**Lanes rebalance before float precision runs out.** Each drop halves the gap it lands in, so after about 50 drops into the same spot two neighbors would become equal. When a drop lands within 1e-6 of another card, the lane is renumbered to 1..n in the same order, and the response carries `X-Lane-Rebalanced: true` so the client refetches the lane. The header is listed in the CORS `ExposeHeaders` (`cmd/api/main.go`), because otherwise a frontend on another origin can't read it.

**One shared helper.** The three per-type `next*Position` functions and the move logic repeated in each `PATCH` handler are now methods on one per-type lane descriptor (`dealLanes`/`leadLanes`/`prospectLanes` in `internal/handlers/card_position.go`). Its queries include soft-deleted rows, so a card restored from Trash can't land on a position that's already taken.

**Position is validated and sorting is stable.** The `PATCH` move endpoints now reject a `position` outside ±1e9 (422). `utils.ApplySort` adds `id` as a tie-breaker in the same direction, so cards sharing a position, or rows sharing any sort value, come back in a stable order across pages.

Frontend: after a move whose response has `X-Lane-Rebalanced: true`, refetch that lane before computing the next midpoint.

Regression-guarded: `tests/card_position_test.go`. Spec: `api-system-spec.md` §3 (new `PATCH /leads/:id/status` row), §4 (new `PATCH /prospects/:id/status` row), §7.1.

## 2026-09-22 — Company Size default seed: added "คน" unit, new "> 100 คน" bucket

`DefaultCompanySizeOptions` (`internal/models/company_config.go`) now carries a "คน" (people) unit suffix on every bucket (`1-10` → `1-10 คน`, etc.) and gained a new `> 100 คน` bucket. `cmd/api/main.go`'s demo Company seed rows were updated to reference the renamed buckets so a fresh dev DB stays internally consistent.

This only changes what a **fresh** database seeds on first run — `CompanySizeOption` is Admin-owned, live-edited data (`/admin/company-sizes`), so an already-seeded environment's rows aren't renamed or backfilled by this change; an Admin edits them the same way as any other option list, from `pages/admin/pipeline-config.vue`'s Company tab.

## 2026-09-16 — Top Referrers report, Contract status validation, bulk payment schedule, FK existence checks

Four follow-ups from the last two features:

**Top Referrers report** — `GET`/`GET .../export /reports/top-referrers` (Admin/Sales Manager, same group as `lead-source-conversion`). Per referring Company/Contact (`Lead.referred_by_type`/`referred_by_id`), counts Leads referred, Deals created (`deals.lead_id`), Deals Won, and total Won revenue. Resolves the polymorphic referrer's name via a double-`LEFT JOIN` against `companies`/`contacts` keyed by `referred_by_type` — no existing pattern in this codebase resolves a polymorphic type+id pair to a name in SQL, so this is a new (standard) shape. Closes the "capture only" fast-follow left open when `referred_by_type`/`referred_by_id` shipped.

**Contract status validation** — `models.IsValidContractStatus` (mirroring `Payment`'s own `IsValidPaymentMethod`) now validates `status` on both `Create` and `Update`; previously any string was accepted with no check at all, not even enum membership. `quote_id`, if set, is now also checked to exist and to belong to the same Deal as the Contract (`404`/`422` respectively) via a new shared `validateContractForm`.

**Bulk payment schedule generation** — `POST /deals/:dealId/payment-installments/bulk`, mirroring `CampaignHandler.BulkCreateTasks`'s batch-insert-plus-one-audit-log-entry shape, so a rep generating an N-installment schedule doesn't produce N separate audit-log rows the way N calls to the single-row `POST` would.

**FK existence checks** — `Lead.company_id` (optional) and `Lead.referred_by_id` are now existence-checked against their target tables (previously accepted any id unchecked, including ones that don't exist at all) — `validateLeadCompanyID`/extended `validateReferredBy` in `internal/handlers/leads.go`. Deal/Contact's own required `company_id`/`contact_id` remain presence-checked only (a separate, wider, deliberately out-of-scope inconsistency, left alone here).

Regression-guarded: `tests/top_referrers_test.go`, `tests/contract_validation_test.go`, new cases in `tests/payment_installment_test.go`/`tests/lead_company_test.go`/`tests/lead_referred_by_test.go`. Spec: `api-system-spec.md` §3, §7.5a, §8.1, §8.4.

## 2026-09-15 — Payment Installment schedule, Outstanding Balance aging, and a new reminder rule

Added `PaymentInstallment` (`internal/models/payment_installment.go`) — a planned installment (amount + due date) a rep defines on a Won Deal before money actually arrives, distinct from `Payment` (which only records money already received). No stored status: every read derives paid/partial/overdue/upcoming via a new shared helper, `utils.ComputeInstallmentStatuses` (`internal/utils/payment_schedule.go`) — a cumulative **waterfall** allocation against the Deal's actual Payment total (sorted by due date, earliest first), not an explicit link between one Payment and one installment (that reconciliation model was confirmed with the business owner over the more precise but more invasive alternative).

New CRUD: `GET`/`POST /deals/:dealId/payment-installments`, `PUT`/`DELETE /payment-installments/:id` (`internal/handlers/payment_installments.go`), same `dealForSubResource`/`CanWrite` RBAC as Payments/Quotes/Contracts.

`GET /reports/outstanding-balance` (and its CSV export) now returns an `aging` field per row (`overdue`/`upcoming`/`none`) — `applyOutstandingBalanceAging` batches every relevant Deal's installments in one query and runs the waterfall helper in Go, not N+1 per-row lookups. A Deal with no schedule keeps this report's original flat behavior (`aging: "none"`).

New `NotificationRule` entity type `payment_installment` (`checkPaymentInstallmentDueRule`, `internal/notifier/workflow_rules.go`) fires once per non-fully-paid installment due within `ThresholdDays` — covers both "coming due" and "overdue" in one condition, same shape every other rule already uses. `NotificationRule.entity_type`'s column was widened `varchar(16)` → `varchar(32)` since `"payment_installment"` (20 chars) didn't fit. Batches every matching Deal's total-paid in one grouped query rather than one `SUM` per Deal inside the loop — same batching reasoning as `applyOutstandingBalanceAging` above, just for the reminder checker's own 15-minute ticker instead of a live request.

Regression-guarded: `internal/utils/payment_schedule_test.go` (waterfall allocation), `tests/payment_installment_test.go` (CRUD/RBAC/validation), `internal/notifier/payment_installment_rule_test.go` (reminder firing/dedup). Spec: `api-system-spec.md` §7.5a (schedule CRUD), §8.4 (Outstanding Balance `aging`), §8.8 (`payment_installment` notification rule).

While updating the spec doc above, also corrected an unrelated stale entry found alongside it: §7.7 Campaigns' `POST /campaigns/:id/tasks` row still documented the retired `company_ids`-only request body — `CampaignHandler.BulkCreateTasks` has taken a `targets: {related_type, related_id}[]` shape (Company/Lead/Contact, not just Company) since an earlier, undocumented change. No code changed here, doc only.

## 2026-09-15 — Lead gains `referred_by_type`/`referred_by_id`

Added two optional, both-or-neither fields to `Lead` (`internal/models/lead.go`): `ReferredByType` and `ReferredByID`, capturing which existing Company or Contact referred a Lead in — previously the only place to note that was the free-text `Notes` field. `ReferredByType` is typed as `models.ActivityRelatedType` itself (not a bare string), matching every other enum field on this struct (`Source`, `Status`, `BusinessUnit`), restricted to `RelatedTypeCompany`/`RelatedTypeContact` via a new `models.IsValidReferrerType` (`activity.go`, alongside `ActivityRelatedType`'s own definition — the same restrict-the-enum-to-a-subset pattern `IsValidCampaignTargetType` already established for Campaign targets). Validated in both `Create` and `Update` (`internal/handlers/leads.go`'s new `validateReferredBy`): setting one without the other is `422`. Neither field is checked for existence against its referenced table, matching `company_id`'s own unchecked convention on this same model. No reporting yet — the existing Lead Source Conversion report groups by `source` string only; drilling into a specific referrer needs a new endpoint, deliberately out of scope here. Regression-guarded: `tests/lead_referred_by_test.go`. Spec: `api-system-spec.md` §3.

## 2026-09-15 — `GET /leads` gains `only_converted`

Added `only_converted=true` to `applyLeadLikeFilters` (`internal/handlers/filters.go`), the mirror image of the existing `exclude_converted=true`: returns only Leads with `converted_deal_id IS NOT NULL`. Backs the frontend's new "Converted" scope tab on the Leads list (`pages/crm/leads/index.vue`), so reps can see already-converted Leads and the live stage of the Deal each one became, instead of only ever filtering them out. Shared with `GET /prospects` via the same helper, though no caller passes it there yet. Spec: `api-system-spec.md` §3.

## 2026-09-11 — Open API user manual

Added `docs/OPEN_API_GUIDE.md` — an integrator-facing walkthrough of the Open API below (getting a key as an Admin, authenticating, per-endpoint request/response examples, the full error-code table, a curl quick start, and an FAQ), linked from `README.md`'s API overview and `biz_spec/api-system-spec.md` §8.9. Written up front the precise Update semantics an integrator would otherwise have to read the handler code to discover: `PUT` is a full replace field-by-field (an omitted field is cleared, not preserved) except `status` on both resources and `company_id` on Contact (kept if omitted) — and specifically flags `Contact.is_primary` as the sharpest edge, since omitting it on an update un-sets it with no "leave unchanged" default.

## 2026-09-11 — Open API for external Company/Contact create/update/read

Added a machine-to-machine credential, separate from the staff Bearer-JWT login flow, so an external integration (marketing tool, another CRM, a sync job) can create/read/update `Company`/`Contact` without a staff login: `X-API-Key` header, checked by new `middleware.RequireAPIKey` against a new `api_keys` table (`models.APIKey`, sha256-hashed key, never stored/logged in recoverable form). Admin-only management under `/admin/api-keys` (`GET`/`POST` to list/create, `POST /:id/revoke` to deactivate without losing the row's audit trail); `POST`'s response returns the raw key exactly once.

Every key **acts as** an existing, active `owner_user_id` — `RequireAPIKey` populates the same request-scoped `user_id`/`role` a normal request from that staff member would carry, so the new `/open/companies`/`/open/contacts` routes reuse the exact same `CompanyHandler`/`ContactHandler` methods the staff-facing `/companies`/`/contacts` routes use (`List`/`Create`/`Get`/`Update` only — no Delete/Trash/Restore/bulk, and no other resource), with identical validation and real `created_by`/`updated_by` attribution rather than a separate code path. Rate-limited per key (not per source IP) at 300 req/min.

One routing gotcha worth flagging for future route additions: the new `/open` group has to be registered **before** `authed := api.Group("", middleware.RequireAuth(...), ...)` in `internal/routes/routes.go`. Fiber's `Group(prefix, middlewares...)` registers those middlewares as a `Use(prefix, ...)` matched against every request path with that prefix in registration order — since `authed`'s prefix is `""`, it would otherwise catch every route added after it under `/api/v1`, `/open/*` included, and 401 on the missing Bearer token before `RequireAPIKey`/the API-key check ever ran.

New `internal/models/api_key.go`, `internal/utils/apikey.go`, `internal/middleware/apikey.go` (plus a `apiKeyCache`/`ResetForTests` entry mirroring `authcache.go`'s pattern), `internal/handlers/api_keys.go`. `api_keys` added to `database.AutoMigrate` and `testutil`'s truncate list. Regression-guarded: `tests/open_api_test.go`. Spec: §8.9.

## 2026-09-10 — Company-scoped Activity on Deal/Lead/Prospect status change, Activity backdating, test-DB hardening

**`Company.last_activity_at` now reflects a Deal/Lead/Prospect status change, not just a manually-logged Activity.** `DealHandler.Update`/`UpdateStage`, `LeadHandler.Update`/`Convert`, and `ProspectHandler.Update`/`Convert` each now write a `type: "note"` company-scoped Activity (`utils.LogCompanyActivity`, `internal/utils/activity_log.go`) inside the same transaction as the save, gated on `stage`/`status` actually changing (a no-op "edit this record" resubmit of the same value logs nothing) — this is the first thing that made the dormant-customer/upsell-targeting feature's `last_activity_at` actually reflect a rep's Kanban/status-change work, not only its own manual entry form. `Deal.Update`/`UpdateStage` piggyback on the existing `oldStage != deal.Stage` gate already used for the `stage_changed` audit entry; Lead/Prospect `Update` capture `oldStatus` up front the same way. New `ActivityType` value `"note"`. Regression-guarded: `tests/company_activity_on_stage_change_test.go`. Spec: §4, §7.2.

**`POST /activities` accepts an optional `created_at`** to backdate a manually-logged Activity (e.g. "mark as contacted on `<past date>`" from the Company page) — future dates rejected with `422`; omitted, the usual GORM current-time stamp applies unchanged. Regression-guarded: `tests/activity_backdate_test.go`. Spec: §7.2.

**Hardened `utils.EnsureActiveIndustry` against a concurrent-create race:** `IndustryOption.name` is `uniqueIndex`'d, so two requests racing to auto-register the exact same brand-new industry could have the loser's `Create` fail on the unique constraint and surface as a `500` instead of the no-op it should be. `EnsureActiveIndustry` now re-resolves by name after a failed `Create` and treats a since-appeared row as success. New `internal/utils/pipeline_config_test.go` (external `utils_test` package, needed to avoid a `testutil`→`utils` import cycle) covers create/reactivate/idempotent-recall directly.

**Fixed a real test-DB leakage bug the industry-free-text work surfaced:** `industry_options`/`company_size_options`/`revenue_size_options`/`job_title_options`/`product_category_options` all have a `uniqueIndex`'d `name` but — unlike `PipelineStage`/`LeadSourceOption`/`ProspectSourceOption`/`ProspectStage`, which are deliberately seeded once and excluded from `TruncateAll` — nothing seeds a default set for these and they were simply left out of `internal/testutil`'s truncate list entirely. Any test creating one of these rows (this PR's own new industry tests included) leaked it across every later test run against the same `sales_system_test` database, so a second run could 500 on the same unique-constraint violation `EnsureActiveIndustry`'s hardening above was meant to make survivable — the same class of bug the `notification_rules`/`notification_logs` comment in that file already describes for its own uniqueIndex'd `name`. All five now truncated between tests.

## 2026-09-10 — Company `industry` is free text again, not an `IndustryOption` whitelist

Bug: the frontend Company form's Industry field was changed to a free-typed combobox (any value, not a fixed dropdown), but `CompanyHandler.Create`/`Update` still rejected any value that wasn't already an active `IndustryOption` row (`422 industry is not a valid active industry`) — every custom industry typed by a user failed to save.

Replaced that reject-if-unknown check with `utils.EnsureActiveIndustry` (`internal/utils/pipeline_config.go`): on Create/Update, a non-empty `industry` value is now looked up and, if it doesn't already exist, inserted as a new active `IndustryOption` row (or reactivated, if an Admin had previously deactivated a row with that exact name) rather than rejected. `size`/`revenue_size` keep their existing strict validation against `CompanySizeOption`/`RevenueSizeOption` — this change is industry-only. `/admin/industries` remains for an Admin to curate the resulting list (rename/deactivate), it's just no longer the gate on what a Company can be saved with. Regenerated Swagger (`swag@v1.16.6`) for the updated Create/Update descriptions. New `tests/company_industry_freetext_test.go`. Spec: §4, §8.8.

## 2026-09-10 — Trash `search` filter for Company/Contact/Deal/Lead

`utils.GenericTrash` (the shared handler behind `GET /companies/trash`, `/contacts/trash`, `/deals/trash`, `/leads/trash`, `/prospects/trash`, `/users/trash`) now takes an optional variadic `searchColumns ...string`. When the caller supplies at least one and the request carries `?search=`, it matches (`ILIKE`, OR'd across columns) — Company/Contact/Lead search `name`, Deal searches `title`. Callers that pass none (Prospect, User) leave `?search=` a no-op, same as before this param existed — no behavior change for them. New `tests/trash_search_test.go`. Spec: §2, §7.


## 2026-09-10 — Deal/Lead/Product RBAC gaps, dashboard ambiguous-column fix, notifier/middleware unit tests, Swagger expansion

Follow-up audit on the same day's terminal-stage-exclusivity/dashboard-date-validation/Swagger-scaffold work below.

**Fixed the ambiguous-column bug flagged (not fixed) in that same entry:** `industryBreakdown` — and, it turned out, every other `Summary` aggregate query built off `DashboardHandler.baseFilter` — threw `column reference "created_at" is ambiguous` (and, once traced further, the same for `"status"`) whenever a `date_from`/`date_to` filter was combined with `?company_tag=`. `baseFilter` joins `companies` only when `company_tag` is supplied, and `Company`/`Deal` both embed `AuditedModel`, so `created_at` (and `Company.status`/`Deal.status`, same name, different meaning) became ambiguous to Postgres the moment that join was present. Every affected query previously discarded `Scan`'s error return, so the symptom was silent — a filtered dashboard quietly zeroing out instead of erroring. All `created_at`/`status`/`value`/`assigned_to`/`stage`/`probability` references inside `baseFilter` and the goroutines it feeds (`stageBreakdown`, `industryBreakdown`, `teamPerformance`, Summary's own inline aggregates) are now qualified `deals.`/`companies.` explicitly. New `TestDashboardSummary_DateRangeWithCompanyTagFilter` (`tests/dashboard_date_validation_test.go`) asserts the real `open_pipeline_value` figure, not just a `200`, since the prior failure mode was a silently-zeroed response rather than an error. Spec: §9.

**Fixed real RBAC gaps found via a systematic route-by-route pass** (the pattern of finding these reactively, one PR at a time, made a full pass worth doing): (1) `/deals` — list/create/get/update/delete/stage-move, plus the nested `/deals/:dealId/{quotes,payments,contracts}` sub-resources — had no role check at all, open to every authenticated role including Marketing/Production, despite §1.7 stating those two roles have "no access to Leads/Deals/any other resource." Now gated `salesPipelineRoles` (Admin/Sales Rep/Sales Manager), same set already used for `PATCH /deals/:id/reassign`'s stricter Admin/Sales-Manager-only subset. (2) `GET/POST /leads` and `DELETE /leads/:id` had the same missing-guard bug the 2026-09-09 entry below fixed for `PUT`/`convert` only — now also `salesPipelineRoles`; `GET /leads/:id` stays open (Marketing's "View Lead" read access, unchanged). (3) `POST/PATCH /products` and `PATCH /products/:id/deactivate` were open to any authenticated role despite §8.2 stating "Product Catalog CRUD (Admin only)" — now `adminOnly`; `GET /products` stays open (dropdown/line-item use, same list-open/write-admin split as the §8.8 option-list resources). Regression-guarded: `TestRBAC_DealsSalesPipelineOnly`, `TestRBAC_LeadListCreateDeleteSalesPipelineOnly`, `TestRBAC_ProductCatalogWritesAdminOnly` (`tests/rbac_test.go`). Spec: §1.7, §3, §7, §8.2.

**Added unit tests for two previously-untested packages:** `internal/notifier` and `internal/middleware` had zero package-local tests — coverage only came indirectly through `tests/`' full HTTP round trips, a slow feedback loop for bugs local to either package (both had real, subtle bugs fixed recently: the terminal-stage-exclusivity and Disqualified-rename bugs referenced below). New `internal/notifier/workflow_rules_test.go` reproduces both bug scenarios directly against `checkProspectStaleRule`, plus covers `alreadyNotified`/`recordNotified` dedup and `recipientEmails` resolution. New `internal/middleware/auth_unit_test.go` (no DB) covers `RequireRoles`/`IsManager`/`CurrentUserID`/`CurrentRole` and the auth/must-change-password caches' TTL and invalidation; `internal/middleware/auth_db_test.go` (external `middleware_test` package, needed to avoid a `testutil`→`middleware` import cycle) covers `RequireAuth`/`RequirePasswordChanged` against a real DB — a deactivated account's token, and a stale-`TokenVersion` token, are still rejected.

**Fixed a latent cross-package test race this surfaced:** `internal/testutil.App` was previously safe only because a single package (`tests`) ever called it; adding DB-backed tests to `internal/middleware` and `internal/notifier` meant `go test ./...`'s default per-package parallelism could run two of those packages' test binaries at once against the same shared `sales_system_test` database, racing one test's `TruncateAll` (`RESTART IDENTITY`) against another's in-flight insert — surfaced as a sporadic `duplicate key value violates unique constraint` on a serial primary key, which would have been flaky in CI (`go test ./... -v -race`, no `-p 1`). `testutil.App` now holds a Postgres session-level advisory lock (`pg_advisory_lock`) for the duration of each test via `t.Cleanup`, serializing every caller across processes rather than just within one. Verified stable across repeated `go test ./...` and `go test ./... -race` runs.

**Expanded the Swagger scaffold** from 5 documented paths (`/admin/pipeline-stages`, `/admin/prospect-stages`, `/dashboard/summary`) to 105 paths / 151 operations, covering Leads, Prospects, Deals (+ Quotes/Payments/Contracts), Companies/Contacts (+ CSV import), Users, Tags, Products/Projects, Reports (+ exports), Audit log, Settings, Sales Targets, and the remaining §8.8 admin config-list resources. Not yet covered: Activities, Attachments, Tasks, Campaigns, Notification rules/log, Lead-scoring criteria, Auth, `/team-members`, `/uploads/:key`, and Prospect sources (structurally identical to Lead sources, same pattern applies whenever it's done) — left for a follow-up pass, same incremental approach the scaffold's original entry below described. `biz_spec/api-system-spec.md` remains the authoritative, hand-curated reference; the generated `docs/swagger.json`/`swagger.yaml` regenerated via the same pinned `swag@v1.16.6` command. Spec: §1.1.

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

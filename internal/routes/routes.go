package routes

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/docs"
	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/handlers"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// clientIP resolves the real client address for rate-limiting purposes. This
// app's only deployment target is Railway (railway.toml/Dockerfile), which
// always sits in front as a reverse proxy and sets X-Forwarded-For to the
// actual client IP on every inbound request — c.IP() alone would return
// Railway's own edge address for every request in that setup, collapsing all
// users onto one shared rate-limit bucket (see loginLimiter below) instead of
// limiting each caller independently. Falls back to c.IP() when the header is
// absent (local dev, docker-compose, or any direct, non-proxied connection).
// Take the leftmost hop — Railway's edge sets/overwrites this header itself
// rather than trusting a client-supplied one, so the leftmost entry is the
// original caller even if further proxies appended their own hops after it.
func clientIP(c *fiber.Ctx) string {
	if xff := c.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	return c.IP()
}

// swaggerUIHTML renders swagger-ui-dist (CDN-hosted, not a Go dependency)
// against the embedded /swagger/doc.json — see docs.JSON's doc for why this
// is a static page instead of the swaggo/fiber-swagger middleware.
const swaggerUIHTML = `<!DOCTYPE html>
<html>
<head>
  <title>Sales System API — Swagger UI</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => SwaggerUIBundle({ url: "/swagger/doc.json", dom_id: "#swagger-ui" })
  </script>
</body>
</html>`

// Setup registers every route under /api/v1 — api-system-spec.md. storage
// backs Quote/Contract/Attachment uploads and the /uploads download route —
// see biz_spec/s3-migration-plan.md and utils.Storage.
func Setup(app *fiber.App, db *gorm.DB, cfg *config.Config, storage utils.Storage) {
	authH := handlers.NewAuthHandler(db, cfg)
	userH := handlers.NewUserHandler(db)
	leadH := handlers.NewLeadHandler(db)
	prospectH := handlers.NewProspectHandler(db)
	companyH := handlers.NewCompanyHandler(db)
	contactH := handlers.NewContactHandler(db)
	importH := handlers.NewImportHandler(db)
	dealH := handlers.NewDealHandler(db)
	activityH := handlers.NewActivityHandler(db)
	tagH := handlers.NewTagHandler(db)
	quoteH := handlers.NewQuoteHandler(db, storage)
	paymentH := handlers.NewPaymentHandler(db)
	taskH := handlers.NewTaskHandler(db)
	campaignH := handlers.NewCampaignHandler(db)
	contractH := handlers.NewContractHandler(db, storage)
	productH := handlers.NewProductHandler(db)
	projectH := handlers.NewProjectHandler(db)
	exportH := handlers.NewExportHandler(db)
	reportH := handlers.NewReportHandler(db)
	auditLogH := handlers.NewAuditLogHandler(db)
	dashboardH := handlers.NewDashboardHandler(db)
	attachmentH := handlers.NewAttachmentHandler(db, storage)
	pipelineStageH := handlers.NewPipelineStageHandler(db)
	leadSourceH := handlers.NewLeadSourceHandler(db)
	prospectSourceH := handlers.NewProspectSourceHandler(db)
	prospectStageH := handlers.NewProspectStageHandler(db)
	industryOptionH := handlers.NewIndustryOptionHandler(db)
	companySizeOptionH := handlers.NewCompanySizeOptionHandler(db)
	revenueSizeOptionH := handlers.NewRevenueSizeOptionHandler(db)
	jobTitleOptionH := handlers.NewJobTitleOptionHandler(db)
	productCategoryOptionH := handlers.NewProductCategoryOptionHandler(db)
	leadScoringCriteriaH := handlers.NewLeadScoringCriteriaHandler(db)
	notificationRuleH := handlers.NewNotificationRuleHandler(db)
	notificationLogH := handlers.NewNotificationLogHandler(db)
	settingsH := handlers.NewSettingsHandler(db, cfg)
	salesTargetH := handlers.NewSalesTargetHandler(db)

	// /swagger/index.html — browsable OpenAPI docs generated from handler
	// annotations (see docs.JSON's own doc for the regen command). Currently
	// covers Prospect stage, Pipeline stage, and Dashboard summary as a
	// scaffold — see main.go's @description for the pattern to extend it to
	// the rest of this file's routes. Development-only: unlike
	// biz_spec/api-system-spec.md this isn't hand-curated for external
	// consumption yet, so it stays off in production until coverage is
	// broader. Serves the embedded spec + a CDN-loaded Swagger UI directly
	// (see swaggerUIHTML) rather than depending on swaggo/swag's runtime
	// package — see docs.JSON's doc for why.
	if cfg.AppEnv == "development" {
		app.Get("/swagger/doc.json", func(c *fiber.Ctx) error {
			c.Set("Content-Type", "application/json")
			return c.Send(docs.JSON)
		})
		app.Get("/swagger/*", func(c *fiber.Ctx) error {
			c.Set("Content-Type", "text/html")
			return c.SendString(swaggerUIHTML)
		})
	}

	api := app.Group("/api/v1")

	// Auth — POST /auth/login is the only unauthenticated route, so it's the
	// only one a brute-force credential-stuffing attempt could hit without a
	// token at all. Rate-limit by IP: generous enough for a mistyped password
	// but not for scripted guessing.
	loginLimiter := limiter.New(limiter.Config{
		Max:          10,
		Expiration:   1 * time.Minute,
		KeyGenerator: clientIP,
		LimitReached: func(c *fiber.Ctx) error {
			return utils.ErrorResponse(c, fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Too many login attempts — try again shortly")
		},
	})
	auth := api.Group("/auth")
	auth.Post("/login", loginLimiter, authH.Login)

	// Uploaded files (Quote PDFs, signed Contracts, Attachments) — Storage.Save
	// returns a root-level "/uploads/<key>" URL (not under /api/v1), so this is
	// registered on app directly rather than inside the authed group below.
	// Previously nothing served this path at all — SaveUpload's returned URLs
	// were dead links regardless of deployment. These are business documents,
	// so require auth (any authenticated role, matching the export/PDF
	// endpoints' access level) rather than serving them unauthenticated.
	//
	// Streams through storage.Open rather than fiber's Static/os-backed
	// serving — the backend may be S3 (see utils.Storage), and this keeps the
	// same auth gate in front of a download regardless of where the bytes
	// actually live, matching the "proxy, not presigned URLs" design in
	// biz_spec/s3-migration-plan.md.
	app.Use("/uploads", middleware.RequireAuth(cfg, db))
	app.Get("/uploads/:key", func(c *fiber.Ctx) error {
		f, err := storage.Open(c.Params("key"))
		if err != nil {
			return utils.NotFound(c, "File not found")
		}
		defer f.Close()
		c.Set(fiber.HeaderContentDisposition, "attachment")
		return c.SendStream(f)
	})

	authed := api.Group("", middleware.RequireAuth(cfg, db), middleware.RequirePasswordChanged(db))

	authed.Post("/auth/logout", authH.Logout)
	authed.Get("/auth/me", authH.Me)
	authed.Post("/auth/change-password", authH.ChangePassword)

	// Users — Admin only, except /team-members.
	adminOnly := middleware.RequireRoles(models.RoleAdmin)
	users := authed.Group("/users", adminOnly)
	users.Get("/", userH.List)
	users.Post("/", userH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	users.Get("/trash", userH.Trash)
	users.Get("/:id", userH.Get)
	users.Put("/:id", userH.Update)
	users.Delete("/:id", userH.Delete)
	users.Post("/:id/restore", userH.Restore)
	authed.Get("/team-members", userH.TeamMembers)

	// Leads
	bulkRoles := middleware.RequireRoles(models.RoleAdmin, models.RoleSalesManager)
	// Sales-pipeline roles only for actual Lead mutations (update/convert) —
	// Marketing has no nav access to /crm/leads (deliberately, per
	// user-story.md §4: "Production is not a full user of this CRM" mirrors
	// Marketing's own Prospect-only scope, FR-CRM-105/106) but could still
	// reach a specific Lead via the Prospect "View Lead" link once converted,
	// and until this fix these two routes had no role check at all — the
	// frontend's Mark SQL/Convert to Deal buttons were only ever hidden by
	// convention, not actually blocked, so a Marketing (or Production) caller
	// hitting either endpoint directly would have succeeded. GET stays open
	// (that's the View Lead read access this is meant to preserve).
	salesPipelineRoles := middleware.RequireRoles(models.RoleAdmin, models.RoleSalesRep, models.RoleSalesManager)
	leads := authed.Group("/leads")
	leads.Get("/", leadH.List)
	leads.Post("/", leadH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	leads.Get("/trash", bulkRoles, leadH.Trash)
	leads.Patch("/bulk-reassign", bulkRoles, leadH.BulkReassign)
	leads.Patch("/bulk-tag", bulkRoles, leadH.BulkTag)
	leads.Patch("/bulk-archive", bulkRoles, leadH.BulkArchive)
	leads.Get("/:id", leadH.Get)
	leads.Put("/:id", salesPipelineRoles, leadH.Update)
	leads.Delete("/:id", leadH.Delete)
	leads.Post("/:id/convert", salesPipelineRoles, leadH.Convert)
	leads.Post("/:id/restore", bulkRoles, leadH.Restore)

	// Prospects — the pre-Lead marketing funnel entity. Admin/Sales Manager/
	// Sales Rep get full read+write access (Sales Reps work Prospects ahead
	// of the Lead hand-off the same way they work Leads/Deals); Marketing
	// owns it day-to-day. Bulk/trash/restore stay on the existing
	// Admin/Sales-Manager-only bulkRoles, same as Leads.
	prospectRoles := middleware.RequireRoles(models.RoleAdmin, models.RoleMarketing, models.RoleSalesManager, models.RoleSalesRep)
	prospects := authed.Group("/prospects", prospectRoles)
	prospects.Get("/", prospectH.List)
	prospects.Post("/", prospectH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	prospects.Get("/trash", bulkRoles, prospectH.Trash)
	prospects.Patch("/bulk-reassign", bulkRoles, prospectH.BulkReassign)
	prospects.Patch("/bulk-tag", bulkRoles, prospectH.BulkTag)
	prospects.Patch("/bulk-archive", bulkRoles, prospectH.BulkArchive)
	prospects.Get("/:id", prospectH.Get)
	prospects.Put("/:id", prospectH.Update)
	prospects.Delete("/:id", prospectH.Delete)
	prospects.Post("/:id/convert", prospectH.Convert)
	prospects.Post("/:id/restore", bulkRoles, prospectH.Restore)

	// Companies
	companies := authed.Group("/companies")
	companies.Get("/", companyH.List)
	companies.Post("/", companyH.Create)
	companies.Post("/import", importH.ImportCompanies)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	companies.Get("/trash", bulkRoles, companyH.Trash)
	companies.Get("/export", bulkRoles, exportH.Companies)
	companies.Get("/:id", companyH.Get)
	companies.Put("/:id", companyH.Update)
	companies.Delete("/:id", companyH.Delete)
	companies.Post("/:id/restore", bulkRoles, companyH.Restore)
	companies.Get("/:companyId/products", productH.ListForCompany)
	companies.Post("/:companyId/products", productH.AddForCompany)
	companies.Get("/:companyId/projects", projectH.ListForCompany)
	companies.Post("/:companyId/projects", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesRep, models.RoleSalesManager), projectH.Create)

	// Contacts
	contacts := authed.Group("/contacts")
	contacts.Get("/", contactH.List)
	contacts.Post("/", contactH.Create)
	contacts.Post("/import", importH.ImportContacts)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	contacts.Get("/trash", bulkRoles, contactH.Trash)
	contacts.Get("/export", bulkRoles, exportH.Contacts)
	contacts.Get("/:id", contactH.Get)
	contacts.Put("/:id", contactH.Update)
	contacts.Delete("/:id", contactH.Delete)
	contacts.Post("/:id/restore", bulkRoles, contactH.Restore)

	// Deals
	deals := authed.Group("/deals")
	deals.Get("/", dealH.List)
	deals.Post("/", dealH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	deals.Get("/trash", bulkRoles, dealH.Trash)
	deals.Patch("/bulk-reassign", bulkRoles, dealH.BulkReassign)
	deals.Patch("/bulk-tag", bulkRoles, dealH.BulkTag)
	deals.Patch("/bulk-archive", bulkRoles, dealH.BulkArchive)
	deals.Get("/export", bulkRoles, exportH.Deals)
	deals.Get("/:id", dealH.Get)
	deals.Put("/:id", dealH.Update)
	deals.Delete("/:id", dealH.Delete)
	deals.Patch("/:id/stage", dealH.UpdateStage)
	deals.Patch("/:id/reassign", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesManager), dealH.Reassign)
	deals.Post("/:id/restore", bulkRoles, dealH.Restore)
	deals.Get("/:dealId/quotes", quoteH.List)
	deals.Post("/:dealId/quotes", quoteH.Create)
	deals.Post("/:dealId/quotes/upload", quoteH.Upload)
	deals.Get("/:dealId/payments", paymentH.List)
	deals.Post("/:dealId/payments", paymentH.Create)
	deals.Get("/:dealId/contracts", contractH.List)
	deals.Post("/:dealId/contracts", contractH.Create)

	// Activities
	activities := authed.Group("/activities")
	activities.Get("/", activityH.List)
	activities.Post("/", activityH.Create)
	activities.Delete("/:id", activityH.Delete)

	// Attachments — Sales/Admin can upload (not Production), any authenticated
	// role can list; Delete's own-uploader-or-manager check is field-level
	// inside the handler (mirrors Activity's CanWrite pattern).
	attachments := authed.Group("/attachments")
	attachments.Get("/", attachmentH.List)
	attachments.Post("/", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesRep, models.RoleSalesManager), attachmentH.Create)
	attachments.Delete("/:id", attachmentH.Delete)

	// Tags — shared taxonomy used across Companies/Deals/Contacts; List stays
	// open to every authenticated role (tag pickers need it), but writes are
	// Admin/Sales-Manager only (bulkRoles) — a Sales Rep renaming or
	// deactivating a shared tag would silently break filtering/reporting for
	// everyone else, the same reasoning PipelineStage/LeadSource are gated on.
	tags := authed.Group("/tags")
	tags.Get("/", tagH.List)
	tags.Post("/", bulkRoles, tagH.Create)
	tags.Put("/:id", bulkRoles, tagH.Update)
	tags.Delete("/:id", bulkRoles, tagH.Delete)

	// Quotes / Payments / Contracts (top-level, non-nested routes)
	authed.Put("/quotes/:id", quoteH.Update)
	authed.Delete("/quotes/:id", quoteH.Delete)
	authed.Get("/quotes/:id/export-pdf", quoteH.ExportPDF)
	authed.Delete("/payments/:id", paymentH.Delete)
	authed.Put("/contracts/:id", contractH.Update)
	authed.Post("/contracts/:id/upload", contractH.Upload)
	authed.Get("/contracts/:id/export-pdf", contractH.ExportPDF)

	// Tasks
	tasks := authed.Group("/tasks")
	tasks.Get("/", taskH.List)
	tasks.Post("/", taskH.Create)
	// Not bulkRoles-gated like Deals'/Leads' bulk endpoints — ownership is
	// enforced per row inside the handlers instead (CanWrite), same as
	// Toggle/Delete below, since a Sales Rep bulk-acting on their own tasks
	// is the primary use case for a personal task list.
	tasks.Patch("/bulk-mark-done", taskH.BulkMarkDone)
	tasks.Patch("/bulk-reassign", taskH.BulkReassign)
	tasks.Patch("/:id/toggle", taskH.Toggle)
	tasks.Patch("/:id", taskH.Update)
	tasks.Delete("/:id", taskH.Delete)

	// Campaigns — named, typed batches of outreach (e.g. dormant-company
	// win-back) that group Tasks via Task.campaign_id. Deliberately not
	// role-gated, same as the /tasks group above and for the same reason:
	// BulkCreateTasks enforces ownership per assignee via CanWrite rather
	// than restricting who may launch a campaign, since both Sales and
	// Marketing (via the guided /crm/campaigns/new flow) are meant to
	// self-serve this.
	campaigns := authed.Group("/campaigns")
	campaigns.Get("/", campaignH.List)
	campaigns.Post("/", campaignH.Create)
	campaigns.Post("/:id/tasks", campaignH.BulkCreateTasks)
	campaigns.Get("/:id/progress", campaignH.Progress)

	// Products — any authenticated role manages the shared catalog.
	products := authed.Group("/products")
	products.Get("/", productH.List)
	products.Post("/", productH.Create)
	products.Get("/export", bulkRoles, exportH.Products)
	products.Patch("/:id", productH.Update)
	products.Patch("/:id/deactivate", productH.Deactivate)

	// Customer-Product link — any authenticated (mirrors AddForCompany's access level).
	authed.Patch("/customer-products/:id", productH.UpdateCustomerProduct)

	// Projects — field-level RBAC enforced inside the handler.
	authed.Get("/projects", projectH.List)
	authed.Get("/projects/export", bulkRoles, exportH.Projects)
	authed.Patch("/projects/:id", projectH.Update)

	// Reports — Sales Manager/Admin only.
	reports := authed.Group("/reports", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesManager))
	reports.Get("/lead-source-conversion", reportH.LeadSourceConversion)
	reports.Get("/lead-source-conversion/export", reportH.LeadSourceConversionExport)
	reports.Get("/customers-by-product-status", reportH.CustomersByProductStatus)
	reports.Get("/customers-by-product-status/export", reportH.CustomersByProductStatusExport)
	reports.Get("/win-loss-reasons", reportH.WinLossReasons)
	reports.Get("/win-loss-reasons/export", reportH.WinLossReasonsExport)
	reports.Get("/stalled-deals", reportH.StalledDeals)
	reports.Get("/stalled-deals/export", reportH.StalledDealsExport)
	reports.Get("/outstanding-balance", reportH.OutstandingBalance)
	reports.Get("/outstanding-balance/export", reportH.OutstandingBalanceExport)
	reports.Get("/quotes-expiring-soon", reportH.QuotesExpiringSoon)
	reports.Get("/quotes-expiring-soon/export", reportH.QuotesExpiringSoonExport)
	reports.Get("/contracts-stuck", reportH.ContractsStuck)
	reports.Get("/contracts-stuck/export", reportH.ContractsStuckExport)
	reports.Get("/projects-at-risk", reportH.ProjectsAtRisk)
	reports.Get("/projects-at-risk/export", reportH.ProjectsAtRiskExport)
	reports.Get("/sales-cycle", reportH.SalesCycle)

	// Marketing's own funnel report — deliberately NOT under the `reports`
	// group above (Sales Manager/Admin only): Marketing has no access to any
	// Deal/Lead-derived report, but does need visibility into its own
	// Prospect-source conversion, the one funnel it actually owns. Sales Rep
	// is included here too, matching `prospectRoles` above.
	prospectReports := authed.Group("/reports", prospectRoles)
	prospectReports.Get("/prospect-source-conversion", reportH.ProspectSourceConversion)
	prospectReports.Get("/prospect-source-conversion/export", reportH.ProspectSourceConversionExport)

	// Audit log — read-only (NFR-007). Full/unrestricted browsing (any
	// entity_type, actor_id, date range) stays Admin-only; List itself
	// further restricts non-Admin callers to a fixed slice of Deal history
	// (entity_type=deal) — see its own comment — so Sales Rep/Sales Manager
	// can pull that in as read-only context on the Activities/Deal-detail
	// pages without gaining the Admin audit viewer's full reach. Sales
	// Manager's slice is wider than Sales Rep's (also includes
	// reassigned/bulk_reassigned, for the Deal detail page's Owner History
	// card — FR-CRM-025/M-8).
	authed.Get("/audit-log", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesRep, models.RoleSalesManager), auditLogH.List)

	// Pipeline stages / lead sources — config writes are Admin-only, replacing
	// the previously hardcoded DealStage/LeadSource enums as the source of
	// truth. List is open to every authenticated role instead, same as
	// /team-members below — every role's own Deal/Lead create/edit forms and
	// the shared Dashboard/Kanban board need these for their stage/source
	// dropdowns, not just Admin. **Fixed 2026-09-09**: List was previously
	// inside the same Admin-only group as the writes, so any non-Admin role
	// landing on a page that fetches these (the Dashboard chief among them)
	// got a silent 403 — no visible breakage since every affected dropdown
	// just quietly rendered with zero/stale options, but it still surfaced as
	// a stray "not authorized" toast on pages that route failed requests
	// through a shared error handler (e.g. pages/index.vue's dashboard).
	authed.Get("/admin/pipeline-stages", pipelineStageH.List)
	pipelineStages := authed.Group("/admin/pipeline-stages", adminOnly)
	pipelineStages.Post("/", pipelineStageH.Create)
	pipelineStages.Patch("/:id", pipelineStageH.Update)
	pipelineStages.Delete("/:id", pipelineStageH.Delete)

	authed.Get("/admin/lead-sources", leadSourceH.List)
	leadSources := authed.Group("/admin/lead-sources", adminOnly)
	leadSources.Post("/", leadSourceH.Create)
	leadSources.Patch("/:id", leadSourceH.Update)
	leadSources.Delete("/:id", leadSourceH.Delete)

	// Prospect sources — Marketing's own funnel-source list; writes are
	// Admin-only, same as every other /admin/* config resource here (Marketing
	// manages day-to-day Prospect data via /prospects*, not this taxonomy).
	// List is open to every authenticated role — same 2026-09-09 fix as
	// pipeline-stages/lead-sources/product-categories above:
	// pages/crm/prospects/index.vue|[id].vue|create.vue (reachable by
	// Marketing, Marketing's OWN primary page, not Admin-gated) fetch this for
	// their source dropdown/filter, so Marketing got a silent 403 loading
	// its own core page — the worst instance of this bug, since it broke the
	// one role's primary daily workflow entirely, not just a secondary widget.
	authed.Get("/admin/prospect-sources", prospectSourceH.List)
	prospectSources := authed.Group("/admin/prospect-sources", adminOnly)
	prospectSources.Post("/", prospectSourceH.Create)
	prospectSources.Patch("/:id", prospectSourceH.Update)
	prospectSources.Delete("/:id", prospectSourceH.Delete)

	// Prospect stages — Marketing's own funnel stage list, replacing the
	// previously hardcoded ProspectStatus working-stage enum ("Converted" is
	// excluded from this table, see ProspectStage's own doc). Same
	// list-open/writes-admin-only shape as every other pipeline-config
	// resource above.
	authed.Get("/admin/prospect-stages", prospectStageH.List)
	prospectStages := authed.Group("/admin/prospect-stages", adminOnly)
	prospectStages.Post("/", prospectStageH.Create)
	prospectStages.Patch("/:id", prospectStageH.Update)
	prospectStages.Delete("/:id", prospectStageH.Delete)

	// Company industry / size — writes are Admin-only, replacing the
	// previously frontend-only hardcoded INDUSTRY_OPTIONS list (and Size's
	// total lack of one). List open to every role, same 2026-09-09 fix:
	// pages/crm/companies/create.vue|[id].vue|index.vue (not Admin-gated,
	// reachable by every role with Company access) fetch these for their
	// industry/size dropdowns.
	authed.Get("/admin/industries", industryOptionH.List)
	industries := authed.Group("/admin/industries", adminOnly)
	industries.Post("/", industryOptionH.Create)
	industries.Patch("/:id", industryOptionH.Update)
	industries.Delete("/:id", industryOptionH.Delete)

	authed.Get("/admin/company-sizes", companySizeOptionH.List)
	companySizes := authed.Group("/admin/company-sizes", adminOnly)
	companySizes.Post("/", companySizeOptionH.Create)
	companySizes.Patch("/:id", companySizeOptionH.Update)
	companySizes.Delete("/:id", companySizeOptionH.Delete)

	authed.Get("/admin/revenue-sizes", revenueSizeOptionH.List)
	revenueSizes := authed.Group("/admin/revenue-sizes", adminOnly)
	revenueSizes.Post("/", revenueSizeOptionH.Create)
	revenueSizes.Patch("/:id", revenueSizeOptionH.Update)
	revenueSizes.Delete("/:id", revenueSizeOptionH.Delete)

	// Contact job titles — writes are Admin-only, same treatment as
	// Industry/Size (previously pure free text with no controlled list at
	// all). List open to every role, same 2026-09-09 fix:
	// pages/crm/contacts/create.vue|[id].vue fetch this for their job-title
	// dropdown.
	authed.Get("/admin/job-titles", jobTitleOptionH.List)
	jobTitles := authed.Group("/admin/job-titles", adminOnly)
	jobTitles.Post("/", jobTitleOptionH.Create)
	jobTitles.Patch("/:id", jobTitleOptionH.Update)
	jobTitles.Delete("/:id", jobTitleOptionH.Delete)

	// List open to every authenticated role (not just Admin) — same reasoning
	// and same 2026-09-09 fix as pipeline-stages/lead-sources above:
	// pages/crm/projects/index.vue's Products tab (reachable by every role,
	// not Admin-gated) fetches this for its category dropdown, so a
	// non-Admin role — Production in particular, just landing on this page
	// via the Dashboard's own "Projects Needing a Status Update" deep link —
	// got a silent 403 here, which the app's axios interceptor turns into a
	// hard redirect away from the very page it was trying to reach.
	authed.Get("/admin/product-categories", productCategoryOptionH.List)
	productCategories := authed.Group("/admin/product-categories", adminOnly)
	productCategories.Post("/", productCategoryOptionH.Create)
	productCategories.Patch("/:id", productCategoryOptionH.Update)
	productCategories.Delete("/:id", productCategoryOptionH.Delete)

	// Lead scoring criteria — Admin-only config, FR-CRM-006.
	leadScoringCriteria := authed.Group("/admin/lead-scoring-criteria", adminOnly)
	leadScoringCriteria.Get("/", leadScoringCriteriaH.List)
	leadScoringCriteria.Post("/", leadScoringCriteriaH.Create)
	leadScoringCriteria.Patch("/:id", leadScoringCriteriaH.Update)
	leadScoringCriteria.Delete("/:id", leadScoringCriteriaH.Delete)

	// Workflow notification rules — Admin-only config, FR-CRM-100/101/102.
	notificationRules := authed.Group("/admin/notification-rules", adminOnly)
	notificationRules.Get("/", notificationRuleH.List)
	notificationRules.Post("/", notificationRuleH.Create)
	notificationRules.Patch("/:id", notificationRuleH.Update)
	notificationRules.Delete("/:id", notificationRuleH.Delete)

	// Recent rule firings, in-app — any authenticated role; per-row CanWrite
	// scoping happens inside the handler, not via adminOnly/RequireRoles.
	authed.Get("/notification-log", notificationLogH.List)

	// App settings (e.g. quarterly sales quota) — Admin-only config,
	// FR-CRM-058.
	settings := authed.Group("/admin/settings", adminOnly)
	settings.Get("/", settingsH.Get)
	settings.Patch("/", settingsH.Update)

	// Per-quarter/per-year sales targets — Admin-only config, FR-CRM-092.
	// Overrides AppSettings.QuarterlySalesTarget/4 for a specific period.
	salesTargets := authed.Group("/admin/sales-targets", adminOnly)
	salesTargets.Get("/", salesTargetH.List)
	salesTargets.Post("/", salesTargetH.Create)
	salesTargets.Patch("/:id", salesTargetH.Update)
	salesTargets.Delete("/:id", salesTargetH.Delete)

	// Dashboard
	authed.Get("/dashboard/summary", dashboardH.Summary)
	// Marketing's own dashboard tab (FR-CRM-107) — not role-gated at the
	// route, same as the Deal-centric summary above; the frontend decides
	// which role sees which tab.
	authed.Get("/dashboard/prospect-summary", dashboardH.ProspectSummary)
}

package routes

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/docs"
	"github.com/igeargeek/sales-system-api/internal/clientip"
	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/handlers"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

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
// see biz_spec/s3-migration-plan.md and utils.Storage. proxies keys the
// login rate limiter (see internal/clientip).
func Setup(app *fiber.App, db *gorm.DB, cfg *config.Config, storage utils.Storage, proxies *clientip.Resolver) {
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
	quoteTemplateH := handlers.NewQuoteTemplateHandler(db)
	paymentH := handlers.NewPaymentHandler(db)
	paymentInstallmentH := handlers.NewPaymentInstallmentHandler(db)
	taskH := handlers.NewTaskHandler(db)
	campaignH := handlers.NewCampaignHandler(db)
	contractH := handlers.NewContractHandler(db, storage)
	productH := handlers.NewProductHandler(db)
	projectH := handlers.NewProjectHandler(db)
	exportH := handlers.NewExportHandler(db)
	reportH := handlers.NewReportHandler(db)
	auditLogH := handlers.NewAuditLogHandler(db)
	dashboardH := handlers.NewDashboardHandler(db)
	pipelineOverviewH := handlers.NewPipelineOverviewHandler(db)
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
	apiKeyH := handlers.NewAPIKeyHandler(db)
	openOptionsH := handlers.NewOpenOptionsHandler(db)

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
	// but not for scripted guessing. Behind Railway's edge proxy the socket
	// peer is the proxy, so the key comes from X-Forwarded-For — read only
	// from TRUSTED_PROXIES peers, right to left (see internal/clientip).
	loginLimiter := limiter.New(limiter.Config{
		Max:          10,
		Expiration:   1 * time.Minute,
		KeyGenerator: proxies.ClientIP,
		LimitReached: func(c *fiber.Ctx) error {
			return utils.ErrorResponse(c, fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Too many login attempts — try again shortly")
		},
	})
	auth := api.Group("/auth")
	auth.Post("/login", loginLimiter, authH.Login)

	// Uploaded files (Quote PDFs, signed Contracts, Attachments) — Storage.Save
	// returns a root-level "/uploads/<key>" URL (not under /api/v1), so this is
	// registered on app directly rather than inside the authed group below.
	// These are business documents, so require auth (any authenticated role, matching the export/PDF
	// endpoints' access level) rather than serving them unauthenticated.
	//
	// Streams through storage.Open rather than fiber's Static/os-backed
	// serving — the backend may be S3 (see utils.Storage), and this keeps the
	// same auth gate in front of a download regardless of where the bytes
	// actually live, matching the "proxy, not presigned URLs" design in
	// biz_spec/s3-migration-plan.md.
	//
	// RequirePasswordChanged too, same as `authed` below — otherwise an
	// account still on an Admin-assigned password could download documents
	// it can't reach through any /api/v1 route yet.
	app.Use("/uploads", middleware.RequireAuth(cfg, db), middleware.RequirePasswordChanged(db))
	app.Get("/uploads/:key", func(c *fiber.Ctx) error {
		f, err := storage.Open(c.Params("key"))
		if err != nil {
			return utils.NotFound(c, "File not found")
		}
		defer f.Close()
		c.Set(fiber.HeaderContentDisposition, "attachment")
		return c.SendStream(f)
	})

	// Open API — external/integration access to Company, Contact, Project,
	// Product, Prospect, and Lead, plus a read-only Deal payment schedule —
	// the resources partner systems most commonly need to sync (CRM/marketing
	// tool sources of truth, plus the pipeline entities feeding them). Authenticated by X-API-Key
	// (RequireAPIKey) instead of the staff Bearer-JWT flow `authed` below,
	// since a server-to-server caller has no user session to log in as; the
	// key acts as its configured owner_user_id, so these reuse the exact same
	// handler methods `authed`'s own resource groups use (same validation,
	// same created_by/updated_by attribution, same CanWrite ownership rules
	// for Prospect/Lead) rather than duplicating that logic. Deliberately
	// excludes Delete/Trash/Restore/bulk/Convert endpoints and every other
	// resource — scope is create/update/read only (read only for Deal
	// payment schedules).
	//
	// Registered BEFORE `authed` below rather than alongside it: `authed :=
	// api.Group("", middleware.RequireAuth(...), ...)` registers those
	// middlewares as a fiber.Use("/api/v1", ...) catch-all — since fiber
	// matches middleware in registration order against every overlapping
	// path, any route added under `api` AFTER that point (even one not built
	// off the `authed` variable, like this group) would still be forced
	// through RequireAuth's Bearer-JWT check first. Registering this group
	// earlier in the stack keeps it on its own X-API-Key gate only.
	//
	// Rate-limited per key (not per IP, unlike loginLimiter — many
	// integration calls legitimately come from one shared egress IP) so one
	// runaway/misconfigured integration can't exhaust capacity shared with
	// every other key or the staff-facing API.
	//
	// Keyed on the validated API-key ID (CurrentAPIKeyID), not the raw
	// X-API-Key header value — and RequireAPIKey now runs BEFORE this
	// limiter, not after: an unauthenticated caller spraying garbage/random
	// key values would otherwise each mint their own distinct rate-limit
	// bucket (the raw header string) before ever being rejected, an
	// unbounded-memory-growth vector the limiter's own storage has no
	// defense against. Running auth first means every invalid key is
	// rejected with a 401 before it ever reaches the limiter at all; the one
	// case CurrentAPIKeyID can still come back unset (which never actually
	// happens given this ordering) falls back to the raw header so the
	// limiter still has *something* to key on rather than panicking.
	openLimiter := limiter.New(limiter.Config{
		Max:        300,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			if keyID, ok := middleware.CurrentAPIKeyID(c); ok {
				return strconv.FormatUint(uint64(keyID), 10)
			}
			return c.Get("X-API-Key")
		},
		LimitReached: func(c *fiber.Ctx) error {
			return utils.ErrorResponse(c, fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Too many requests — try again shortly")
		},
	})
	open := api.Group("/open", middleware.RequireAPIKey(db), openLimiter, middleware.LogOpenAPIWrites(db))

	open.Get("/options", openOptionsH.List)

	// Idempotency-Key support (middleware.RequireIdempotency) is only wired
	// onto Create — the endpoint where a client retrying a timed-out call
	// risks a duplicate Company/Contact; List/Get/Update have no such risk
	// (a re-sent Update just re-applies the same full-replace it always
	// would).
	idempotency := middleware.RequireIdempotency(db)

	// Sales-pipeline roles — every role but Production (Marketing has Sales
	// Rep parity, feature-spec.md FR-CRM-123). Declared ahead of the Open API
	// so an API key owned by a Production user gets the same 403s on
	// Companies/Contacts/Leads/Prospects/Deals as the staff routes.
	salesPipelineRoles := middleware.RequireRoles(models.SalesPipelineRoles...)

	openCompanies := open.Group("/companies", salesPipelineRoles)
	openCompanies.Get("/", companyH.List)
	openCompanies.Post("/", idempotency, companyH.Create)
	openCompanies.Get("/:id", companyH.Get)
	openCompanies.Put("/:id", companyH.Update)

	openContacts := open.Group("/contacts", salesPipelineRoles)
	openContacts.Get("/", contactH.List)
	openContacts.Post("/", idempotency, contactH.Create)
	openContacts.Get("/:id", contactH.Get)
	openContacts.Put("/:id", contactH.Update)

	// Projects — List/Update are the same handler methods the staff-facing
	// /projects routes use; Create is a dedicated CreateOpen (company_id in
	// the body instead of a path segment, since there's no company-scoped
	// nesting here). Update is PATCH, matching ProjectHandler.Update's own
	// partial-update semantics (not a full-replace PUT like Company/Contact).
	openProjects := open.Group("/projects")
	openProjects.Get("/", projectH.List)
	openProjects.Post("/", idempotency, projectH.CreateOpen)
	openProjects.Get("/:id", projectH.Get)
	openProjects.Patch("/:id", projectH.Update)

	// Products — List/Create/Update are already top-level (not
	// company-nested), so these reuse the exact same ProductHandler methods
	// the staff-facing /products routes use, same as Company/Contact above.
	openProducts := open.Group("/products")
	openProducts.Get("/", productH.List)
	openProducts.Post("/", idempotency, productH.Create)
	openProducts.Get("/:id", productH.Get)
	openProducts.Patch("/:id", productH.Update)

	// Prospects — full List/Create/Get/Update already exist top-level;
	// reused as-is (same CanWrite ownership rule the staff /prospects routes
	// enforce, evaluated against the API key's owner_user_id/role).
	openProspects := open.Group("/prospects", salesPipelineRoles)
	openProspects.Get("/", prospectH.List)
	openProspects.Post("/", idempotency, prospectH.Create)
	openProspects.Get("/:id", prospectH.Get)
	openProspects.Put("/:id", prospectH.Update)

	// Leads — same treatment as Prospects above.
	openLeads := open.Group("/leads", salesPipelineRoles)
	openLeads.Get("/", leadH.List)
	openLeads.Post("/", idempotency, leadH.Create)
	openLeads.Get("/:id", leadH.Get)
	openLeads.Put("/:id", leadH.Update)

	// Deal payment schedules — read-only, the one Deal sub-resource exposed
	// here, so an integration can follow a Project's deal_id to its planned
	// installments and their derived paid/partial/overdue/upcoming status.
	// Reuses PaymentInstallmentHandler.List as-is, so the staff route's
	// access rules carry over unchanged: the same salesPipelineRoles gate the
	// staff /deals group applies (Production keys 403), and
	// dealForSubResource's CanWrite check — a key acting as anyone but an
	// Admin/Sales Manager (a Sales Rep or Marketing user) only sees schedules
	// on Deals assigned to that user (or unassigned), unlike Prospect/Lead
	// reads above.
	openDeals := open.Group("/deals", salesPipelineRoles)
	openDeals.Get("/:dealId/payment-installments", paymentInstallmentH.List)

	authed := api.Group("", middleware.RequireAuth(cfg, db), middleware.RequirePasswordChanged(db))

	authed.Post("/auth/logout", authH.Logout)
	authed.Get("/auth/me", authH.Me)
	authed.Post("/auth/change-password", authH.ChangePassword)

	// Role gates. salesPipelineRoles (declared above the Open API group) is
	// every role but Production. managerRoles (Admin/Sales Manager) guards
	// bulk actions, Trash/Restore, exports, Company/Contact delete and merge,
	// Deal reassign, tag writes and the /reports group.
	adminOnly := middleware.RequireRoles(models.RoleAdmin)
	managerRoles := middleware.RequireRoles(models.RoleAdmin, models.RoleSalesManager)

	// Users — Admin only, except /team-members.
	users := authed.Group("/users", adminOnly)
	users.Get("/", userH.List)
	users.Post("/", userH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	users.Get("/trash", userH.Trash)
	users.Patch("/bulk-activate", userH.BulkActivate)
	users.Patch("/bulk-deactivate", userH.BulkDeactivate)
	users.Get("/:id", userH.Get)
	users.Put("/:id", userH.Update)
	users.Delete("/:id", userH.Delete)
	users.Post("/:id/restore", userH.Restore)
	authed.Get("/team-members", userH.TeamMembers)

	// Leads — salesPipelineRoles on every route except the single-record
	// reads, which stay open so Production can follow a link to a Lead.
	leads := authed.Group("/leads")
	leads.Get("/", salesPipelineRoles, leadH.List)
	leads.Post("/", salesPipelineRoles, leadH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	leads.Get("/trash", managerRoles, leadH.Trash)
	leads.Patch("/bulk-reassign", managerRoles, leadH.BulkReassign)
	leads.Patch("/bulk-tag", managerRoles, leadH.BulkTag)
	leads.Patch("/bulk-archive", managerRoles, leadH.BulkArchive)
	leads.Get("/:id", leadH.Get)
	// Same gating as Get above (no extra role restriction beyond `authed`) —
	// this is read-only detail about a Lead a caller can already view.
	leads.Get("/:id/score-breakdown", leadH.ScoreBreakdown)
	leads.Put("/:id", salesPipelineRoles, leadH.Update)
	// Kanban drag-and-drop's own narrow-PATCH move endpoint (status+position
	// only) — see leadH.UpdateStatus's doc comment for why this exists
	// alongside the full-record PUT above.
	leads.Patch("/:id/status", salesPipelineRoles, leadH.UpdateStatus)
	leads.Delete("/:id", salesPipelineRoles, leadH.Delete)
	leads.Post("/:id/convert", salesPipelineRoles, leadH.Convert)
	leads.Post("/:id/restore", managerRoles, leadH.Restore)

	// Prospects — the pre-Lead marketing funnel, owned day-to-day by
	// Marketing; every salesPipelineRoles role reads and writes it.
	prospects := authed.Group("/prospects", salesPipelineRoles)
	prospects.Get("/", prospectH.List)
	prospects.Post("/", prospectH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	prospects.Get("/trash", managerRoles, prospectH.Trash)
	prospects.Patch("/bulk-reassign", managerRoles, prospectH.BulkReassign)
	prospects.Patch("/bulk-tag", managerRoles, prospectH.BulkTag)
	prospects.Patch("/bulk-archive", managerRoles, prospectH.BulkArchive)
	prospects.Get("/:id", prospectH.Get)
	prospects.Put("/:id", prospectH.Update)
	// Kanban drag-and-drop's own narrow-PATCH move endpoint (status+position
	// only) — see prospectH.UpdateStatus's doc comment for why this exists
	// alongside the full-record PUT above.
	prospects.Patch("/:id/status", prospectH.UpdateStatus)
	prospects.Delete("/:id", prospectH.Delete)
	prospects.Post("/:id/convert", prospectH.Convert)
	prospects.Post("/:id/restore", managerRoles, prospectH.Restore)

	// Companies — salesPipelineRoles (Production has no access, spec §1.7;
	// its Projects page reads company_name off GET /projects instead).
	companies := authed.Group("/companies", salesPipelineRoles)
	companies.Get("/", companyH.List)
	companies.Post("/", companyH.Create)
	companies.Post("/import", importH.ImportCompanies)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	companies.Get("/trash", managerRoles, companyH.Trash)
	companies.Get("/export", managerRoles, exportH.Companies)
	companies.Get("/:id", companyH.Get)
	companies.Put("/:id", companyH.Update)
	companies.Delete("/:id", managerRoles, companyH.Delete)
	companies.Post("/:id/restore", managerRoles, companyH.Restore)
	companies.Post("/:id/merge", managerRoles, companyH.Merge)
	companies.Get("/:companyId/products", productH.ListForCompany)
	companies.Post("/:companyId/products", productH.AddForCompany)
	companies.Get("/:companyId/projects", projectH.ListForCompany)
	companies.Post("/:companyId/projects", projectH.Create)

	// Contacts — same gates as Companies.
	contacts := authed.Group("/contacts", salesPipelineRoles)
	contacts.Get("/", contactH.List)
	contacts.Post("/", contactH.Create)
	contacts.Post("/import", importH.ImportContacts)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	contacts.Get("/trash", managerRoles, contactH.Trash)
	contacts.Get("/export", managerRoles, exportH.Contacts)
	contacts.Get("/:id", contactH.Get)
	contacts.Put("/:id", contactH.Update)
	contacts.Delete("/:id", managerRoles, contactH.Delete)
	contacts.Post("/:id/restore", managerRoles, contactH.Restore)
	contacts.Post("/:id/merge", managerRoles, contactH.Merge)

	// Deals — salesPipelineRoles, including every Quote/Payment/Contract
	// sub-resource nested under a Deal (Production has no access, spec §1.7).
	deals := authed.Group("/deals", salesPipelineRoles)
	deals.Get("/", dealH.List)
	deals.Post("/", dealH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	deals.Get("/trash", managerRoles, dealH.Trash)
	deals.Patch("/bulk-reassign", managerRoles, dealH.BulkReassign)
	deals.Patch("/bulk-tag", managerRoles, dealH.BulkTag)
	deals.Patch("/bulk-archive", managerRoles, dealH.BulkArchive)
	deals.Get("/export", managerRoles, exportH.Deals)
	deals.Get("/:id", dealH.Get)
	deals.Put("/:id", dealH.Update)
	deals.Delete("/:id", dealH.Delete)
	deals.Patch("/:id/stage", dealH.UpdateStage)
	deals.Patch("/:id/reassign", managerRoles, dealH.Reassign)
	deals.Post("/:id/restore", managerRoles, dealH.Restore)
	deals.Get("/:dealId/quotes", quoteH.List)
	deals.Post("/:dealId/quotes", quoteH.Create)
	deals.Post("/:dealId/quotes/upload", quoteH.Upload)
	deals.Get("/:dealId/payments", paymentH.List)
	deals.Post("/:dealId/payments", paymentH.Create)
	deals.Get("/:dealId/payment-installments", paymentInstallmentH.List)
	deals.Post("/:dealId/payment-installments", paymentInstallmentH.Create)
	deals.Post("/:dealId/payment-installments/bulk", paymentInstallmentH.BulkCreate)
	deals.Get("/:dealId/contracts", contractH.List)
	deals.Post("/:dealId/contracts", contractH.Create)

	// Activities
	activities := authed.Group("/activities")
	activities.Get("/", activityH.List)
	activities.Post("/", activityH.Create)
	activities.Delete("/:id", activityH.Delete)

	// Attachments — Sales/Admin can upload (not Production). List requires a
	// related_type+related_id and checks the caller can read that record, and
	// Create that they can write it (attachmentParentAccess); Delete's
	// own-uploader-or-manager check is field-level inside the handler
	// (mirrors Activity's CanWrite pattern).
	attachments := authed.Group("/attachments")
	attachments.Get("/", attachmentH.List)
	attachments.Post("/", salesPipelineRoles, attachmentH.Create)
	attachments.Delete("/:id", attachmentH.Delete)

	// Tags — shared taxonomy used across Companies/Deals/Contacts; List stays
	// open to every authenticated role (tag pickers need it), but writes are
	// Admin/Sales-Manager only (managerRoles) — a Sales Rep renaming or
	// deactivating a shared tag would silently break filtering/reporting for
	// everyone else, the same reasoning PipelineStage/LeadSource are gated on.
	tags := authed.Group("/tags")
	tags.Get("/", tagH.List)
	tags.Post("/", managerRoles, tagH.Create)
	tags.Put("/:id", managerRoles, tagH.Update)
	tags.Delete("/:id", managerRoles, tagH.Delete)

	// Quote Templates — same salesPipelineRoles access as the deals group
	// above (self-serve for any Sales role, not an Admin-curated library).
	quoteTemplates := authed.Group("/quote-templates", salesPipelineRoles)
	quoteTemplates.Get("/", quoteTemplateH.List)
	quoteTemplates.Post("/", quoteTemplateH.Create)
	quoteTemplates.Delete("/:id", quoteTemplateH.Delete)

	// Quotes / Payments / Contracts (top-level, non-nested routes) — the same
	// salesPipelineRoles gate as the /deals sub-resources they belong to
	// (Production has no Deal access). Search before "/:id".
	authed.Get("/quotes", salesPipelineRoles, quoteH.Search)
	authed.Get("/quotes/:id", salesPipelineRoles, quoteH.Get)
	authed.Put("/quotes/:id", salesPipelineRoles, quoteH.Update)
	authed.Delete("/quotes/:id", salesPipelineRoles, quoteH.Delete)
	authed.Get("/quotes/:id/export-pdf", salesPipelineRoles, quoteH.ExportPDF)
	authed.Post("/quotes/:id/duplicate", salesPipelineRoles, quoteH.Duplicate)
	// Payments CSV — Admin/Sales Manager, like the other exports. Before
	// "/payments/:id".
	authed.Get("/payments/export", managerRoles, exportH.Payments)
	authed.Put("/payments/:id", salesPipelineRoles, paymentH.Update)
	authed.Delete("/payments/:id", salesPipelineRoles, paymentH.Delete)
	authed.Put("/payment-installments/:id", salesPipelineRoles, paymentInstallmentH.Update)
	authed.Delete("/payment-installments/:id", salesPipelineRoles, paymentInstallmentH.Delete)
	authed.Put("/contracts/:id", salesPipelineRoles, contractH.Update)
	authed.Post("/contracts/:id/upload", salesPipelineRoles, contractH.Upload)
	authed.Get("/contracts/:id/export-pdf", salesPipelineRoles, contractH.ExportPDF)

	// Tasks
	tasks := authed.Group("/tasks")
	tasks.Get("/", taskH.List)
	tasks.Post("/", taskH.Create)
	// Not managerRoles-gated like Deals'/Leads' bulk endpoints — ownership is
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

	// Products — spec §8.2: catalog writes are Admin only. List is open to
	// every role, since Deal/Quote line-item forms need the catalog.
	products := authed.Group("/products")
	products.Get("/", productH.List)
	products.Post("/", adminOnly, productH.Create)
	products.Get("/export", managerRoles, exportH.Products)
	products.Patch("/:id", adminOnly, productH.Update)
	products.Patch("/:id/deactivate", adminOnly, productH.Deactivate)

	// Customer-Product link — salesPipelineRoles, same as AddForCompany.
	authed.Patch("/customer-products/:id", salesPipelineRoles, productH.UpdateCustomerProduct)

	// Projects — field-level RBAC enforced inside the handler.
	authed.Get("/projects", projectH.List)
	authed.Get("/projects/export", managerRoles, exportH.Projects)
	authed.Patch("/projects/:id", projectH.Update)

	// Reports — Sales Manager/Admin only.
	reports := authed.Group("/reports", managerRoles)
	reports.Get("/lead-source-conversion", reportH.LeadSourceConversion)
	reports.Get("/lead-source-conversion/export", reportH.LeadSourceConversionExport)
	reports.Get("/source-performance", reportH.SourcePerformance)
	reports.Get("/source-performance/export", reportH.SourcePerformanceExport)
	reports.Get("/top-referrers", reportH.TopReferrers)
	reports.Get("/top-referrers/export", reportH.TopReferrersExport)
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
	// is included too: the same salesPipelineRoles as /prospects.
	prospectReports := authed.Group("/reports", salesPipelineRoles)
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
	authed.Get("/audit-log", salesPipelineRoles, auditLogH.List)

	// Admin config option lists (pipeline stages, lead/prospect sources,
	// prospect stages, industries, sizes, job titles, product categories):
	// writes are Admin only; List is open to every authenticated role, since
	// every role's create/edit forms, filters and the Dashboard read them
	// for their dropdowns.
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

	// Prospect sources — Marketing's funnel-source list (Marketing manages
	// Prospects via /prospects*, not this taxonomy).
	authed.Get("/admin/prospect-sources", prospectSourceH.List)
	prospectSources := authed.Group("/admin/prospect-sources", adminOnly)
	prospectSources.Post("/", prospectSourceH.Create)
	prospectSources.Patch("/:id", prospectSourceH.Update)
	prospectSources.Delete("/:id", prospectSourceH.Delete)

	// Prospect stages — Marketing's working stages ("Converted" isn't in
	// this table; see ProspectStage's doc).
	authed.Get("/admin/prospect-stages", prospectStageH.List)
	prospectStages := authed.Group("/admin/prospect-stages", adminOnly)
	prospectStages.Post("/", prospectStageH.Create)
	prospectStages.Patch("/:id", prospectStageH.Update)
	prospectStages.Delete("/:id", prospectStageH.Delete)

	// Company industry / size / revenue size.
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

	// Contact job titles.
	authed.Get("/admin/job-titles", jobTitleOptionH.List)
	jobTitles := authed.Group("/admin/job-titles", adminOnly)
	jobTitles.Post("/", jobTitleOptionH.Create)
	jobTitles.Patch("/:id", jobTitleOptionH.Update)
	jobTitles.Delete("/:id", jobTitleOptionH.Delete)

	// Product categories (the Projects page's Products tab, open to every
	// role including Production, reads this).
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

	// App settings (e.g. quarterly sales quota) — Admin-only to configure,
	// FR-CRM-058. Get is opened to salesPipelineRoles (not adminOnly) so a
	// Sales Rep's Deal page can read require_signed_contract_before_won and
	// warn before they hit the 422 blocking Won — same "reads open, writes
	// gated" split Tags/PipelineStage already use.
	settings := authed.Group("/admin/settings")
	settings.Get("/", salesPipelineRoles, settingsH.Get)
	settings.Patch("/", adminOnly, settingsH.Update)
	// Weekly digest (FR-CRM-123): preview what Monday's email would say, or
	// send it to yourself; the schedule itself is notifier.StartWeeklyDigest.
	authed.Get("/admin/weekly-digest/preview", adminOnly, settingsH.WeeklyDigestPreview)
	authed.Post("/admin/weekly-digest/test", adminOnly, settingsH.SendWeeklyDigestTest)

	// Per-quarter/per-year sales targets — Admin-only config, FR-CRM-092.
	// Overrides AppSettings.QuarterlySalesTarget/4 for a specific period.
	salesTargets := authed.Group("/admin/sales-targets", adminOnly)
	salesTargets.Get("/", salesTargetH.List)
	salesTargets.Post("/", salesTargetH.Create)
	salesTargets.Patch("/:id", salesTargetH.Update)
	salesTargets.Delete("/:id", salesTargetH.Delete)

	// API keys — Admin-only, credentials for the /open/* integration group
	// below (middleware.RequireAPIKey). Revoke rather than Delete: keeping the
	// row (IsActive=false) preserves who created/revoked it, matching the
	// soft-delete convention elsewhere instead of losing that audit trail.
	apiKeys := authed.Group("/admin/api-keys", adminOnly)
	apiKeys.Get("/", apiKeyH.List)
	apiKeys.Post("/", apiKeyH.Create)
	apiKeys.Post("/:id/revoke", apiKeyH.Revoke)
	apiKeys.Get("/:id/logs", apiKeyH.Logs)

	// Dashboard
	authed.Get("/dashboard/summary", dashboardH.Summary)
	// Marketing's own dashboard tab (FR-CRM-107) — not role-gated at the
	// route, same as the Deal-centric summary above; the frontend decides
	// which role sees which tab.
	authed.Get("/dashboard/prospect-summary", dashboardH.ProspectSummary)
	// Lead stats for the Sales tab — same "not role-gated, frontend decides"
	// convention as the two dashboard routes above.
	authed.Get("/dashboard/lead-summary", dashboardH.LeadSummary)

	// Overview Pipeline (FR-CRM-123) — Prospect/Lead/Deal lanes plus the
	// period summary strip in one payload. Same salesPipelineRoles gate as
	// the Lead/Deal routes it reads from (Production has no access).
	authed.Get("/pipeline/overview", salesPipelineRoles, pipelineOverviewH.Overview)
	// Forecast accuracy history (Commit/Best Case/Pipeline forecast rigor) —
	// same "not role-gated, frontend decides" convention as the routes above.
	authed.Get("/dashboard/forecast-accuracy", dashboardH.ForecastAccuracy)
}

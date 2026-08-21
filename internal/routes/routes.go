package routes

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"gorm.io/gorm"

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

// Setup registers every route under /api/v1 — api-system-spec.md.
func Setup(app *fiber.App, db *gorm.DB, cfg *config.Config) {
	authH := handlers.NewAuthHandler(db, cfg)
	userH := handlers.NewUserHandler(db)
	leadH := handlers.NewLeadHandler(db)
	companyH := handlers.NewCompanyHandler(db)
	contactH := handlers.NewContactHandler(db)
	importH := handlers.NewImportHandler(db)
	dealH := handlers.NewDealHandler(db)
	activityH := handlers.NewActivityHandler(db)
	tagH := handlers.NewTagHandler(db)
	quoteH := handlers.NewQuoteHandler(db)
	paymentH := handlers.NewPaymentHandler(db)
	taskH := handlers.NewTaskHandler(db)
	contractH := handlers.NewContractHandler(db)
	productH := handlers.NewProductHandler(db)
	projectH := handlers.NewProjectHandler(db)
	exportH := handlers.NewExportHandler(db)
	reportH := handlers.NewReportHandler(db)
	auditLogH := handlers.NewAuditLogHandler(db)
	dashboardH := handlers.NewDashboardHandler(db)
	attachmentH := handlers.NewAttachmentHandler(db)
	pipelineStageH := handlers.NewPipelineStageHandler(db)
	leadSourceH := handlers.NewLeadSourceHandler(db)
	leadScoringCriteriaH := handlers.NewLeadScoringCriteriaHandler(db)
	notificationRuleH := handlers.NewNotificationRuleHandler(db)
	notificationLogH := handlers.NewNotificationLogHandler(db)
	settingsH := handlers.NewSettingsHandler(db)
	salesTargetH := handlers.NewSalesTargetHandler(db)

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

	// Uploaded files (Quote PDFs, signed Contracts, Attachments) — utils.SaveUpload
	// returns a root-level "/uploads/<name>" URL (not under /api/v1), so this is
	// registered on app directly rather than inside the authed group below.
	// Previously nothing served this path at all — SaveUpload's returned URLs
	// were dead links regardless of deployment. These are business documents,
	// so require auth (any authenticated role, matching the export/PDF
	// endpoints' access level) rather than serving them unauthenticated.
	app.Use("/uploads", middleware.RequireAuth(cfg))
	app.Static("/uploads", utils.UploadDir, fiber.Static{Download: true})

	authed := api.Group("", middleware.RequireAuth(cfg), middleware.RequirePasswordChanged(db))

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
	leads := authed.Group("/leads")
	leads.Get("/", leadH.List)
	leads.Post("/", leadH.Create)
	// Static routes before "/:id" so e.g. "trash" isn't captured as an id.
	leads.Get("/trash", bulkRoles, leadH.Trash)
	leads.Patch("/bulk-reassign", bulkRoles, leadH.BulkReassign)
	leads.Patch("/bulk-tag", bulkRoles, leadH.BulkTag)
	leads.Patch("/bulk-archive", bulkRoles, leadH.BulkArchive)
	leads.Get("/:id", leadH.Get)
	leads.Put("/:id", leadH.Update)
	leads.Delete("/:id", leadH.Delete)
	leads.Post("/:id/convert", leadH.Convert)
	leads.Post("/:id/restore", bulkRoles, leadH.Restore)

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
	tasks.Delete("/:id", taskH.Delete)

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

	// Audit log — Admin only, read-only (NFR-007).
	authed.Get("/audit-log", adminOnly, auditLogH.List)

	// Pipeline stages / lead sources — Admin-only config, replacing the
	// previously hardcoded DealStage/LeadSource enums as the source of truth.
	pipelineStages := authed.Group("/admin/pipeline-stages", adminOnly)
	pipelineStages.Get("/", pipelineStageH.List)
	pipelineStages.Post("/", pipelineStageH.Create)
	pipelineStages.Patch("/:id", pipelineStageH.Update)
	pipelineStages.Delete("/:id", pipelineStageH.Delete)

	leadSources := authed.Group("/admin/lead-sources", adminOnly)
	leadSources.Get("/", leadSourceH.List)
	leadSources.Post("/", leadSourceH.Create)
	leadSources.Patch("/:id", leadSourceH.Update)
	leadSources.Delete("/:id", leadSourceH.Delete)

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
}

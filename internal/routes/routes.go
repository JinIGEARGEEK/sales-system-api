package routes

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/handlers"
	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
)

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
	reportH := handlers.NewReportHandler(db)
	auditLogH := handlers.NewAuditLogHandler(db)
	dashboardH := handlers.NewDashboardHandler(db)

	api := app.Group("/api/v1")

	// Auth — POST /auth/login is the only unauthenticated route.
	auth := api.Group("/auth")
	auth.Post("/login", authH.Login)

	authed := api.Group("", middleware.RequireAuth(cfg))

	authed.Post("/auth/logout", authH.Logout)
	authed.Get("/auth/me", authH.Me)

	// Users — Admin only, except /team-members.
	adminOnly := middleware.RequireRoles(models.RoleAdmin)
	users := authed.Group("/users", adminOnly)
	users.Get("/", userH.List)
	users.Post("/", userH.Create)
	users.Get("/:id", userH.Get)
	users.Put("/:id", userH.Update)
	users.Delete("/:id", userH.Delete)
	authed.Get("/team-members", userH.TeamMembers)

	// Leads
	leads := authed.Group("/leads")
	leads.Get("/", leadH.List)
	leads.Post("/", leadH.Create)
	leads.Get("/:id", leadH.Get)
	leads.Put("/:id", leadH.Update)
	leads.Delete("/:id", leadH.Delete)
	leads.Post("/:id/convert", leadH.Convert)

	// Companies
	companies := authed.Group("/companies")
	companies.Get("/", companyH.List)
	companies.Post("/", companyH.Create)
	companies.Post("/import", importH.ImportCompanies)
	companies.Get("/:id", companyH.Get)
	companies.Put("/:id", companyH.Update)
	companies.Delete("/:id", companyH.Delete)
	companies.Get("/:companyId/products", productH.ListForCompany)
	companies.Post("/:companyId/products", productH.AddForCompany)
	companies.Get("/:companyId/projects", projectH.ListForCompany)
	companies.Post("/:companyId/projects", projectH.Create, middleware.RequireRoles(models.RoleAdmin, models.RoleSalesRep, models.RoleSalesManager))

	// Contacts
	contacts := authed.Group("/contacts")
	contacts.Get("/", contactH.List)
	contacts.Post("/", contactH.Create)
	contacts.Post("/import", importH.ImportContacts)
	contacts.Get("/:id", contactH.Get)
	contacts.Put("/:id", contactH.Update)
	contacts.Delete("/:id", contactH.Delete)

	// Deals
	deals := authed.Group("/deals")
	deals.Get("/", dealH.List)
	deals.Post("/", dealH.Create)
	deals.Get("/:id", dealH.Get)
	deals.Put("/:id", dealH.Update)
	deals.Delete("/:id", dealH.Delete)
	deals.Patch("/:id/stage", dealH.UpdateStage)
	deals.Patch("/:id/reassign", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesManager), dealH.Reassign)
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

	// Tags
	tags := authed.Group("/tags")
	tags.Get("/", tagH.List)
	tags.Post("/", tagH.Create)
	tags.Put("/:id", tagH.Update)
	tags.Delete("/:id", tagH.Delete)

	// Quotes / Payments / Contracts (top-level, non-nested routes)
	authed.Put("/quotes/:id", quoteH.Update)
	authed.Delete("/quotes/:id", quoteH.Delete)
	authed.Get("/quotes/:id/export-pdf", quoteH.ExportPDF)
	authed.Delete("/payments/:id", paymentH.Delete)
	authed.Put("/contracts/:id", contractH.Update)
	authed.Post("/contracts/:id/upload", contractH.Upload)

	// Tasks
	tasks := authed.Group("/tasks")
	tasks.Get("/", taskH.List)
	tasks.Post("/", taskH.Create)
	tasks.Patch("/:id/toggle", taskH.Toggle)
	tasks.Delete("/:id", taskH.Delete)

	// Products — Admin only.
	products := authed.Group("/products", adminOnly)
	products.Get("/", productH.List)
	products.Post("/", productH.Create)
	products.Patch("/:id/deactivate", productH.Deactivate)

	// Projects — field-level RBAC enforced inside the handler.
	authed.Patch("/projects/:id", projectH.Update)

	// Reports — Sales Manager/Admin only.
	reports := authed.Group("/reports", middleware.RequireRoles(models.RoleAdmin, models.RoleSalesManager))
	reports.Get("/lead-source-conversion", reportH.LeadSourceConversion)
	reports.Get("/customers-by-product-status", reportH.CustomersByProductStatus)

	// Audit log — Admin only, read-only (NFR-007).
	authed.Get("/audit-log", auditLogH.List, adminOnly)

	// Dashboard
	authed.Get("/dashboard/summary", dashboardH.Summary)
}

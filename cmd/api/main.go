package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/notifier"
	"github.com/igeargeek/sales-system-api/internal/routes"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

func main() {
	cfg := config.Load()

	// Deny-by-default: only the explicit "development" env may run with the
	// placeholder secret/wildcard CORS. A misspelled or unset APP_ENV (e.g.
	// "prod" instead of "production") now fails closed instead of silently
	// booting a production-looking deployment with a guessable JWT secret.
	if cfg.AppEnv != "development" {
		if cfg.JWTSecret == "change-me-in-production" {
			log.Fatal("refusing to start outside development with the default JWT_SECRET — set a real secret")
		}
		if cfg.CORSOrigins == "*" {
			log.Fatal("refusing to start outside development with CORS_ORIGINS=\"*\" — set an explicit allow-list")
		}
	}

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	seedAdmin(db)
	seedPipelineConfig(db)
	seedLeadScoringCriteria(db)
	seedAppSettings(db)

	app := fiber.New(fiber.Config{
		ErrorHandler: apiErrorHandler,
	})
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSOrigins,
	}))

	// Unauthenticated — used by the hosting platform's health check (e.g.
	// Railway) to decide whether a deploy is ready to receive traffic.
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	routes.Setup(app, db, cfg)

	// Background job: emails a Task's assignee once its due date has passed.
	// Safe to run even without SMTP configured — see internal/utils/mailer.go.
	notifier.StartTaskDueReminders(db, cfg)
	notifier.StartWorkflowRuleReminders(db, cfg)

	log.Fatal(app.Listen(":" + cfg.Port))
}

// apiErrorHandler guarantees every error — including a panic caught by
// recover.New() or a handler that returns a bare error instead of routing
// through utils.* — still gets the §1.5 JSON envelope instead of Fiber's
// default plain-text response.
func apiErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := err.Error()
	if fe, ok := err.(*fiber.Error); ok {
		code = fe.Code
		message = fe.Message
	}
	// 5xx messages can carry raw Go/GORM/driver error text (internal details
	// that shouldn't reach a client) — log the real error server-side and
	// return a generic message instead. 4xx messages (e.g. Fiber's own
	// "Cannot GET /x") are safe to pass through as-is.
	if code >= fiber.StatusInternalServerError {
		log.Printf("unhandled error on %s %s: %v", c.Method(), c.Path(), err)
		message = "Internal server error"
	}
	return utils.ErrorResponse(c, code, "INTERNAL_ERROR", message)
}

// seedAdmin creates an initial Admin user if the users table is empty so
// there's a way to log in on first run.
func seedAdmin(db *gorm.DB) {
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count > 0 {
		return
	}

	const email = "admin@igeargeek.com"
	password := utils.NewTempPassword()

	hash, err := utils.HashPassword(password)
	if err != nil {
		log.Fatalf("failed to hash seed admin password: %v", err)
	}

	admin := models.User{
		FirstName:          "System",
		LastName:           "Admin",
		Email:              email,
		PasswordHash:       hash,
		Role:               models.RoleAdmin,
		IsActive:           true,
		MustChangePassword: true,
	}
	if err := db.Create(&admin).Error; err != nil {
		log.Fatalf("failed to seed admin user: %v", err)
	}

	log.Printf("Seeded initial admin user — email: %s, password: %s (change this immediately)", email, password)
}

// seedPipelineConfig inserts the default PipelineStage/LeadSourceOption rows
// (the values that used to be hardcoded Go enums) if their tables are empty,
// same first-run-only idiom as seedAdmin above. Existing Deals/Leads keep
// validating fine post-migration because these are the exact strings already
// stored on those rows.
func seedPipelineConfig(db *gorm.DB) {
	var stageCount int64
	db.Model(&models.PipelineStage{}).Count(&stageCount)
	if stageCount == 0 {
		if err := db.Create(&models.DefaultPipelineStages).Error; err != nil {
			log.Fatalf("failed to seed default pipeline stages: %v", err)
		}
		log.Printf("Seeded %d default pipeline stages", len(models.DefaultPipelineStages))
	}

	var sourceCount int64
	db.Model(&models.LeadSourceOption{}).Count(&sourceCount)
	if sourceCount == 0 {
		if err := db.Create(&models.DefaultLeadSourceOptions).Error; err != nil {
			log.Fatalf("failed to seed default lead sources: %v", err)
		}
		log.Printf("Seeded %d default lead sources", len(models.DefaultLeadSourceOptions))
	}

	var industryCount int64
	db.Model(&models.IndustryOption{}).Count(&industryCount)
	if industryCount == 0 {
		if err := db.Create(&models.DefaultIndustryOptions).Error; err != nil {
			log.Fatalf("failed to seed default industries: %v", err)
		}
		log.Printf("Seeded %d default industries", len(models.DefaultIndustryOptions))
	}

	var companySizeCount int64
	db.Model(&models.CompanySizeOption{}).Count(&companySizeCount)
	if companySizeCount == 0 {
		if err := db.Create(&models.DefaultCompanySizeOptions).Error; err != nil {
			log.Fatalf("failed to seed default company sizes: %v", err)
		}
		log.Printf("Seeded %d default company sizes", len(models.DefaultCompanySizeOptions))
	}

	var revenueSizeCount int64
	db.Model(&models.RevenueSizeOption{}).Count(&revenueSizeCount)
	if revenueSizeCount == 0 {
		if err := db.Create(&models.DefaultRevenueSizeOptions).Error; err != nil {
			log.Fatalf("failed to seed default revenue sizes: %v", err)
		}
		log.Printf("Seeded %d default revenue sizes", len(models.DefaultRevenueSizeOptions))
	}

	var jobTitleCount int64
	db.Model(&models.JobTitleOption{}).Count(&jobTitleCount)
	if jobTitleCount == 0 {
		if err := db.Create(&models.DefaultJobTitleOptions).Error; err != nil {
			log.Fatalf("failed to seed default job titles: %v", err)
		}
		log.Printf("Seeded %d default job titles", len(models.DefaultJobTitleOptions))
	}

	var productCategoryCount int64
	db.Model(&models.ProductCategoryOption{}).Count(&productCategoryCount)
	if productCategoryCount == 0 {
		if err := db.Create(&models.DefaultProductCategoryOptions).Error; err != nil {
			log.Fatalf("failed to seed default product categories: %v", err)
		}
		log.Printf("Seeded %d default product categories", len(models.DefaultProductCategoryOptions))
	}
}

// seedLeadScoringCriteria inserts the default LeadScoringCriterion rows if
// the table is empty, same first-run-only idiom as seedPipelineConfig above.
func seedLeadScoringCriteria(db *gorm.DB) {
	var count int64
	db.Model(&models.LeadScoringCriterion{}).Count(&count)
	if count == 0 {
		if err := db.Create(&models.DefaultLeadScoringCriteria).Error; err != nil {
			log.Fatalf("failed to seed default lead scoring criteria: %v", err)
		}
		log.Printf("Seeded %d default lead scoring criteria", len(models.DefaultLeadScoringCriteria))
	}
}

// seedAppSettings inserts the singleton AppSettings row (ID=1) if the table
// is empty, same first-run-only idiom as seedAdmin/seedPipelineConfig above.
func seedAppSettings(db *gorm.DB) {
	var count int64
	db.Model(&models.AppSettings{}).Count(&count)
	if count > 0 {
		return
	}
	if err := db.Create(&models.DefaultAppSettings).Error; err != nil {
		log.Fatalf("failed to seed app settings: %v", err)
	}
	log.Printf("Seeded default app settings (quarterly_sales_target=%d, annual_revenue_goal=%d)", models.DefaultAppSettings.QuarterlySalesTarget, models.DefaultAppSettings.AnnualRevenueGoal)
}

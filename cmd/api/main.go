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
	"github.com/igeargeek/sales-system-api/internal/routes"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

func main() {
	cfg := config.Load()

	if cfg.JWTSecret == "change-me-in-production" && cfg.AppEnv == "production" {
		log.Fatal("refusing to start in production with the default JWT_SECRET — set a real secret")
	}

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	seedAdmin(db)

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

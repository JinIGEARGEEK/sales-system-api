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

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	seedAdmin(db)

	app := fiber.New()
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

	routes.Setup(app, db, cfg)

	log.Fatal(app.Listen(":" + cfg.Port))
}

// seedAdmin creates an initial Admin user if the users table is empty so
// there's a way to log in on first run.
func seedAdmin(db *gorm.DB) {
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count > 0 {
		return
	}

	const username = "admin"
	const password = "admin123"

	hash, err := utils.HashPassword(password)
	if err != nil {
		log.Fatalf("failed to hash seed admin password: %v", err)
	}

	admin := models.User{
		FirstName:    "System",
		LastName:     "Admin",
		Username:     username,
		Email:        "admin@example.com",
		PasswordHash: hash,
		Role:         models.RoleAdmin,
		IsActive:     true,
	}
	if err := db.Create(&admin).Error; err != nil {
		log.Fatalf("failed to seed admin user: %v", err)
	}

	log.Printf("Seeded initial admin user — username: %s, password: %s (change this immediately)", username, password)
}

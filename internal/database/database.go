package database

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

func Connect(cfg *config.Config) (*gorm.DB, error) {
	// Railway/Heroku/Render-style platforms inject a single DATABASE_URL for their
	// managed Postgres add-on rather than discrete DB_HOST/DB_PORT/etc — prefer it
	// when present instead of requiring the platform's env vars to be remapped.
	dsn := cfg.DatabaseURL
	if dsn == "" {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
		)
	}

	// Never fully silent: a Silent logger means real production DB failures
	// (constraint violations, connection drops) leave no server-side trace at
	// all — the handler layer already returns opaque "Failed to X" messages
	// to the client, so this is the only place they'd be visible.
	logLevel := logger.Error
	if cfg.AppEnv == "development" {
		logLevel = logger.Warn
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	return db, nil
}

// AutoMigrate creates/updates every table this API owns. Kept as a single explicit
// list (rather than reflection over a registry) so adding a resource is a one-line diff.
func AutoMigrate(db *gorm.DB) error {
	// GORM's AutoMigrate never drops columns (by design, to avoid accidental data
	// loss), so the retired `username` column — NOT NULL on existing databases —
	// has to be dropped explicitly or every insert against models.User fails.
	if db.Migrator().HasColumn(&models.User{}, "username") {
		if err := db.Migrator().DropColumn(&models.User{}, "username"); err != nil {
			return fmt.Errorf("drop legacy username column: %w", err)
		}
	}
	if db.Migrator().HasColumn(&models.Product{}, "sku") {
		if err := db.Migrator().DropColumn(&models.Product{}, "sku"); err != nil {
			return fmt.Errorf("drop legacy sku column: %w", err)
		}
	}

	if err := db.AutoMigrate(
		&models.User{},
		&models.Lead{},
		&models.Company{},
		&models.Contact{},
		&models.Deal{},
		&models.Activity{},
		&models.Tag{},
		&models.Quote{},
		&models.Payment{},
		&models.Task{},
		&models.Contract{},
		&models.Product{},
		&models.CustomerProduct{},
		&models.Project{},
		&models.AuditLogEntry{},
		&models.Attachment{},
		&models.PipelineStage{},
		&models.LeadSourceOption{},
		&models.AppSettings{},
	); err != nil {
		return err
	}

	return backfillCompanyDomains(db)
}

// backfillCompanyDomains populates the new Company.Domain column (added
// alongside the indexed-domain-lookup optimization in ImportCompanies) for
// any pre-existing row AutoMigrate didn't/couldn't set — the column is new,
// so every row created before this migration has it blank. Idempotent and
// cheap to re-run: it only touches rows where domain is still empty.
func backfillCompanyDomains(db *gorm.DB) error {
	var companies []models.Company
	if err := db.Where("website <> '' AND domain = ''").Find(&companies).Error; err != nil {
		return fmt.Errorf("load companies for domain backfill: %w", err)
	}
	for _, co := range companies {
		domain := utils.ExtractDomain(co.Website)
		if domain == "" {
			continue
		}
		if err := db.Model(&models.Company{}).Where("id = ?", co.ID).Update("domain", domain).Error; err != nil {
			return fmt.Errorf("backfill domain for company %d: %w", co.ID, err)
		}
	}
	return nil
}

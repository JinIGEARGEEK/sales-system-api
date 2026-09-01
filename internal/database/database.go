package database

import (
	"errors"
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
		&models.Prospect{},
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
		&models.IndustryOption{},
		&models.CompanySizeOption{},
		&models.RevenueSizeOption{},
		&models.JobTitleOption{},
		&models.ProductCategoryOption{},
		&models.LeadScoringCriterion{},
		&models.NotificationRule{},
		&models.NotificationLog{},
		&models.AppSettings{},
		&models.SalesTarget{},
		&models.DocumentSequence{},
	); err != nil {
		return err
	}

	if err := backfillCompanyDomains(db); err != nil {
		return err
	}

	// Lead.CompanyID (FK) replaces the old free-text CompanyName column —
	// backfill every existing Lead's link from that text BEFORE dropping the
	// column, same ordering constraint as the username/sku drops above (this
	// one just can't happen at the top since it needs the data first).
	if err := backfillLeadCompanyIDs(db); err != nil {
		return err
	}
	if db.Migrator().HasColumn(&models.Lead{}, "company_name") {
		if err := db.Migrator().DropColumn(&models.Lead{}, "company_name"); err != nil {
			return fmt.Errorf("drop legacy company_name column: %w", err)
		}
	}
	return nil
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

// backfillLeadCompanyIDs links every pre-existing Lead's old free-text
// company_name to a real Company via the new CompanyID column, run once
// (per row) as part of the 2026-08-24 migration off free-text company_name
// entirely. Reads company_name via raw SQL rather than models.Lead, since
// that field no longer exists on the struct by the time this runs — the
// column itself is still physically present (AutoMigrate never drops
// columns, so it's there to read right up until the DropColumn call after
// this) — but couldn't be read through GORM's usual model-scan path.
// Idempotent and cheap to re-run: WHERE company_id IS NULL means every row
// this successfully processes drops out of the query on the next run.
func backfillLeadCompanyIDs(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&models.Lead{}, "company_name") {
		return nil // already dropped by a prior run — nothing left to read
	}

	type leadCompanyName struct {
		ID          uint
		CompanyName string
	}
	var rows []leadCompanyName
	if err := db.Table("leads").
		Select("id, company_name").
		Where("company_id IS NULL AND company_name IS NOT NULL AND company_name <> ''").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("load leads for company_id backfill: %w", err)
	}

	for _, row := range rows {
		var company models.Company
		// Same case/whitespace-insensitive match as stores/companies.ts's
		// findByName getter on the frontend — reuse an existing Company
		// rather than creating a near-duplicate for a differently-cased or
		// padded spelling of the same name.
		err := db.Where("LOWER(TRIM(name)) = LOWER(TRIM(?))", row.CompanyName).First(&company).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			company = models.Company{Name: row.CompanyName, Status: models.StatusActive}
			if err := db.Create(&company).Error; err != nil {
				return fmt.Errorf("create company %q for lead %d backfill: %w", row.CompanyName, row.ID, err)
			}
		} else if err != nil {
			return fmt.Errorf("look up company %q for lead %d backfill: %w", row.CompanyName, row.ID, err)
		}
		if err := db.Model(&models.Lead{}).Where("id = ?", row.ID).Update("company_id", company.ID).Error; err != nil {
			return fmt.Errorf("backfill company_id for lead %d: %w", row.ID, err)
		}
	}
	return nil
}

package database

import (
	"errors"
	"fmt"
	"log"

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
		&models.PaymentInstallment{},
		&models.Task{},
		&models.Campaign{},
		&models.Contract{},
		&models.Product{},
		&models.CustomerProduct{},
		&models.Project{},
		&models.AuditLogEntry{},
		&models.Attachment{},
		&models.PipelineStage{},
		&models.LeadSourceOption{},
		&models.ProspectSourceOption{},
		&models.ProspectStage{},
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
		&models.ForecastSnapshot{},
		&models.QuoteTemplate{},
		&models.APIKey{},
		&models.IdempotencyKey{},
		&models.OpenAPIRequestLog{},
	); err != nil {
		return err
	}

	if err := backfillCompanyDomains(db); err != nil {
		return err
	}
	// Must run after backfillCompanyDomains — it needs Domain already
	// populated on every pre-existing row to check for real conflicts.
	if err := ensureCompanyDomainUniqueIndex(db); err != nil {
		return err
	}
	if err := backfillLowercaseTags(db); err != nil {
		return err
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_companies_industry_lower ON companies (LOWER(industry))`).Error; err != nil {
		return fmt.Errorf("create companies industry lower index: %w", err)
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

// ensureCompanyDomainUniqueIndex backs conflictingCompanyDomain's app-level
// pre-check (companies.go) with a real DB constraint — that check alone is a
// check-then-act race: two concurrent Creates for the same domain can both
// pass it before either commits. This partial unique index (excluding blank
// domains and soft-deleted rows) is what actually makes the second one fail,
// which Create/Update then turn back into the same friendly 409.
//
// Not fatal if it can't be created: a deployment with real pre-existing
// duplicate domains (from before this feature existed) would fail the index
// creation outright, and that's a data problem for a human to resolve (merge
// or clear one side), not something that should block the whole app from
// starting. Logged so it isn't silently unenforced.
func ensureCompanyDomainUniqueIndex(db *gorm.DB) error {
	err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_companies_domain_unique
		ON companies (domain) WHERE domain <> '' AND deleted_at IS NULL
	`).Error
	if err != nil {
		log.Printf("database: could not create unique company domain index (likely pre-existing duplicate domains — needs manual cleanup): %v", err)
	}
	return nil
}

// backfillLowercaseTags normalizes every pre-existing Company/Contact Tags
// array to lowercase/trimmed/deduplicated — the same normalization
// normalizeTags (handlers/companies.go) now applies on every Create/Update —
// so the case-insensitive `?tag=` filter (handlers/filters.go's tagFilter)
// can stay a plain `= ANY(tags)` lookup the GIN tags index can serve,
// instead of an unnest+LOWER() scan needed to also cover not-yet-normalized
// legacy rows. Idempotent and cheap to re-run on an already-normalized row.
func backfillLowercaseTags(db *gorm.DB) error {
	if err := db.Exec(`
		UPDATE companies SET tags = ARRAY(SELECT DISTINCT LOWER(TRIM(t)) FROM unnest(tags) t WHERE TRIM(t) <> '')
		WHERE tags IS NOT NULL AND array_length(tags, 1) > 0
	`).Error; err != nil {
		return fmt.Errorf("backfill lowercase company tags: %w", err)
	}
	if err := db.Exec(`
		UPDATE contacts SET tags = ARRAY(SELECT DISTINCT LOWER(TRIM(t)) FROM unnest(tags) t WHERE TRIM(t) <> '')
		WHERE tags IS NOT NULL AND array_length(tags, 1) > 0
	`).Error; err != nil {
		return fmt.Errorf("backfill lowercase contact tags: %w", err)
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

package database

import (
	"errors"
	"fmt"
	"log"
	"time"

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
		&models.DataMigration{},
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
	if err := BackfillCardPositions(db); err != nil {
		return err
	}
	if err := backfillStageEnteredAt(db); err != nil {
		return err
	}
	if err := MigrateCompanySizeDefaults(db); err != nil {
		return err
	}
	return nil
}

// companySizeLegacyNames maps each current default Company Size name
// (models.DefaultCompanySizeOptions) to the unit-less name it was seeded
// under before 2026-09-22.
var companySizeLegacyNames = map[string]string{
	"1-10 คน":     "1-10",
	"11-50 คน":    "11-50",
	"51-200 คน":   "51-200",
	"201-500 คน":  "201-500",
	"501-1000 คน": "501-1000",
	"1000+ คน":    "1000+",
}

// companySizeCatchAll is the bucket added to the defaults on 2026-09-22. Its
// presence also marks this migration as done (see below).
const companySizeCatchAll = "> 100 คน"

// MigrateCompanySizeDefaults (called from AutoMigrate; exported for its
// test) brings a database seeded before 2026-09-22 up to the current
// Company Size defaults. The defaults are only inserted into an
// empty table (cmd/api/main.go), so existing databases kept the old
// unit-less names and never got the "> 100 คน" bucket.
//
// It renames each old default to its "… คน" name and repoints every Company
// using it (Company.size stores the option's name, and Create/Update reject a
// size that isn't an active option, so a rename without this would break
// saving those Companies), then adds "> 100 คน". All in one transaction,
// via UpdateColumn so no Company's updated_at moves (it's not a user edit).
//
// Runs once: it does nothing if "> 100 คน" already exists in any state
// (including deactivated), so an Admin's later renames are never undone on a
// restart. It also does nothing on an empty table, which the seed handles.
func MigrateCompanySizeDefaults(db *gorm.DB) error {
	var total, catchAll int64
	if err := db.Model(&models.CompanySizeOption{}).Unscoped().Count(&total).Error; err != nil {
		return fmt.Errorf("count company sizes: %w", err)
	}
	if err := db.Model(&models.CompanySizeOption{}).Unscoped().Where("name = ?", companySizeCatchAll).Count(&catchAll).Error; err != nil {
		return fmt.Errorf("check company size catch-all: %w", err)
	}
	if total == 0 || catchAll > 0 {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, def := range models.DefaultCompanySizeOptions {
			legacy, ok := companySizeLegacyNames[def.Name]
			if !ok {
				continue
			}
			var current, old int64
			if err := tx.Model(&models.CompanySizeOption{}).Unscoped().Where("name = ?", def.Name).Count(&current).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.CompanySizeOption{}).Unscoped().Where("name = ?", legacy).Count(&old).Error; err != nil {
				return err
			}
			if old == 0 {
				continue
			}
			// Only rename when the new name is free; either way Companies
			// move to the new name so they stay valid.
			if current == 0 {
				if err := tx.Model(&models.CompanySizeOption{}).Unscoped().Where("name = ?", legacy).UpdateColumn("name", def.Name).Error; err != nil {
					return fmt.Errorf("rename company size %q: %w", legacy, err)
				}
			}
			if err := tx.Model(&models.Company{}).Unscoped().Where("size = ?", legacy).UpdateColumn("size", def.Name).Error; err != nil {
				return fmt.Errorf("repoint companies from size %q: %w", legacy, err)
			}
		}
		if err := tx.Create(&models.CompanySizeOption{Name: companySizeCatchAll, IsActive: true}).Error; err != nil {
			return fmt.Errorf("add company size %q: %w", companySizeCatchAll, err)
		}
		log.Printf("Migrated company sizes to the current defaults (added %q)", companySizeCatchAll)
		return nil
	})
}

// backfillStageEnteredAt populates the new Deal/Lead/Prospect
// stage_entered_at column (see models/stage_entered.go) for rows created
// before it existed. Only touches rows still NULL, so it's safe to re-run on
// every boot. Best available evidence per table:
//   - deals: the latest stage_changed audit row, else updated_at (older stage
//     edits made through the Overview form weren't audited)
//   - leads/prospects: created_at while still in the initial "New" lane (it
//     never moved), else updated_at (status changes were never audited)
//
// updated_at can only be later than the real move, never earlier, so a
// backfilled "days in stage" can under-count but never over-count.
func backfillStageEnteredAt(db *gorm.DB) error {
	stmts := []struct{ table, sql string }{
		{"deals", `
			UPDATE deals d SET stage_entered_at = COALESCE(
				(SELECT MAX(a.created_at) FROM audit_log_entries a
				 WHERE a.entity_type = 'deal' AND a.entity_id = d.id AND a.action = 'stage_changed'),
				d.updated_at)
			WHERE d.stage_entered_at IS NULL`},
		{"leads", `
			UPDATE leads SET stage_entered_at = CASE WHEN status = 'New' THEN created_at ELSE updated_at END
			WHERE stage_entered_at IS NULL`},
		// A converted Lead sits in the Overview's Converted lane from the
		// moment it converted, which its Deal's created_at records exactly.
		// Before 2026-09-23 conversion didn't restamp stage_entered_at when
		// the Lead was already Qualified, so move any earlier value up to
		// the conversion. Idempotent: conversions since then already match.
		{"leads (converted)", `
			UPDATE leads l SET stage_entered_at = d.created_at
			FROM deals d
			WHERE d.id = l.converted_deal_id
			  AND (l.stage_entered_at IS NULL OR l.stage_entered_at < d.created_at)`},
		{"prospects", `
			UPDATE prospects SET stage_entered_at = CASE WHEN status = 'New' THEN created_at ELSE updated_at END
			WHERE stage_entered_at IS NULL`},
	}
	for _, st := range stmts {
		if err := db.Exec(st.sql).Error; err != nil {
			return fmt.Errorf("backfill %s stage_entered_at: %w", st.table, err)
		}
	}
	return nil
}

// runOnce runs fn and records name in data_migrations in one transaction,
// or does nothing if name is already recorded — for data migrations whose
// "already done" state can't be read back off the data itself.
func runOnce(db *gorm.DB, name string, fn func(tx *gorm.DB) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var done int64
		if err := tx.Model(&models.DataMigration{}).Where("name = ?", name).Count(&done).Error; err != nil {
			return fmt.Errorf("check data migration %q: %w", name, err)
		}
		if done > 0 {
			return nil
		}
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Create(&models.DataMigration{Name: name, AppliedAt: time.Now()}).Error
	})
}

// cardPositionsBackfill names BackfillCardPositions' data_migrations row.
const cardPositionsBackfill = "card_positions_backfill"

// BackfillCardPositions (called from AutoMigrate; exported for its test)
// gives every Deal/Lead/Prospect still at position 0 a real Kanban position
// (see Deal.Position's doc comment), appended after the lane's highest
// non-zero position in created_at order. Partitioned per lane (Stage for
// deals, Status for leads/prospects) since positions are only compared
// within a lane.
//
// Runs once (runOnce): 0 is also a valid drag-move result (e.g. the midpoint
// of -1 and 1), so a "still at 0" check alone would move such a card on
// every boot. The single run covers both databases from before the column
// existed (every row at 0) and rows created at 0 by the Lead/Prospect
// conversion paths before they assigned positions.
func BackfillCardPositions(db *gorm.DB) error {
	return runOnce(db, cardPositionsBackfill, func(tx *gorm.DB) error {
		backfills := []struct {
			table     string
			laneField string
		}{
			{"deals", "stage"},
			{"leads", "status"},
			{"prospects", "status"},
		}
		for _, b := range backfills {
			sql := fmt.Sprintf(`
				UPDATE %[1]s SET position = sub.base + sub.rn
				FROM (
					SELECT z.id,
						ROW_NUMBER() OVER (PARTITION BY z.%[2]s ORDER BY z.created_at, z.id) AS rn,
						COALESCE((SELECT MAX(o.position) FROM %[1]s o WHERE o.%[2]s = z.%[2]s AND o.position <> 0), 0) AS base
					FROM %[1]s z WHERE z.position = 0
				) sub
				WHERE %[1]s.id = sub.id`, b.table, b.laneField)
			if err := tx.Exec(sql).Error; err != nil {
				return fmt.Errorf("backfill %s position: %w", b.table, err)
			}
		}
		return nil
	})
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

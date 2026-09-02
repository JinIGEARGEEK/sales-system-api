package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
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

	storageBackend, err := newStorageBackend(cfg)
	if err != nil {
		log.Fatal(err)
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
	if cfg.AppEnv == "development" {
		seedDemoData(db)
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: apiErrorHandler,
	})
	app.Use(recover.New())
	// requestid before logger so every access-log line carries the same ID
	// apiErrorHandler logs below — without this there was no way to
	// correlate an access-log entry with the corresponding server-side error
	// log line for the same request when debugging a production incident.
	// Echoes/generates X-Request-ID; the response header lets a client (or
	// this API's own frontend) report it back for support purposes too.
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} - ${latency} ${method} ${path} reqid=${locals:requestid}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSOrigins,
	}))

	// Unauthenticated — used by the hosting platform's health check (e.g.
	// Railway) to decide whether a deploy is ready to receive traffic.
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	routes.Setup(app, db, cfg, storageBackend)

	// Background job: emails a Task's assignee once its due date has passed.
	// Safe to run even without SMTP configured — see internal/utils/mailer.go.
	notifier.StartTaskDueReminders(db, cfg)
	notifier.StartWorkflowRuleReminders(db, cfg)

	log.Fatal(app.Listen(":" + cfg.Port))
}

// newStorageBackend builds the Storage implementation config.StorageBackend
// selects — see biz_spec/s3-migration-plan.md. Fails fast on a missing S3_*
// var when STORAGE_BACKEND=s3 rather than booting and only discovering the
// gap on the first upload attempt, mirroring the JWT_SECRET/CORS_ORIGINS
// checks above. Deliberately does NOT reject STORAGE_BACKEND=local outside
// development the way the plan's "Config" section suggests — this app's
// only current deployment target already runs on local storage today, and
// making that a hard boot failure would take down that existing deployment
// the moment this ships, not just warn about it. Revisit once S3 is actually
// provisioned there.
func newStorageBackend(cfg *config.Config) (utils.Storage, error) {
	switch cfg.StorageBackend {
	case "", "local":
		return utils.NewLocalStorage(utils.UploadDir), nil
	case "s3":
		missing := map[string]string{
			"S3_BUCKET":            cfg.S3Bucket,
			"S3_REGION":            cfg.S3Region,
			"S3_ACCESS_KEY_ID":     cfg.S3AccessKeyID,
			"S3_SECRET_ACCESS_KEY": cfg.S3SecretAccessKey,
		}
		for name, v := range missing {
			if v == "" {
				return nil, fmt.Errorf("STORAGE_BACKEND=s3 requires %s to be set", name)
			}
		}
		return utils.NewS3Storage(context.Background(), cfg.S3Bucket, cfg.S3Region, cfg.S3Endpoint, cfg.S3AccessKeyID, cfg.S3SecretAccessKey, cfg.S3ForcePathStyle)
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND %q (expected \"local\" or \"s3\")", cfg.StorageBackend)
	}
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
		reqID, _ := c.Locals("requestid").(string)
		log.Printf("unhandled error on %s %s (reqid=%s): %v", c.Method(), c.Path(), reqID, err)
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

	var prospectSourceCount int64
	db.Model(&models.ProspectSourceOption{}).Count(&prospectSourceCount)
	if prospectSourceCount == 0 {
		if err := db.Create(&models.DefaultProspectSourceOptions).Error; err != nil {
			log.Fatalf("failed to seed default prospect sources: %v", err)
		}
		log.Printf("Seeded %d default prospect sources", len(models.DefaultProspectSourceOptions))
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

func uintPtr(v uint) *uint           { return &v }
func timePtr(v time.Time) *time.Time { return &v }

// demoPassword is the login for every seedDemoData staff account — fixed
// (not a random temp password like seedAdmin's) since the whole point of
// this dev-only data is fast, frictionless visual checking, not exercising
// the forced-password-change flow (already covered by the real Admin
// account). Never used outside APP_ENV=development — see the guard in main().
const demoPassword = "Password123!"

// seedDemoData populates a small, realistic chain of sample records —
// Companies -> Contacts -> Prospects -> Leads -> Deals -> Tasks -> Projects —
// so a developer can open any page in the app and see it populated instead
// of empty, without clicking through the UI by hand first. Development only
// (see main()'s cfg.AppEnv guard), idempotent same as every seed* function
// above (skips entirely if any Company already exists), and writes through
// the real Go structs/GORM rather than raw SQL so the shapes stay honest —
// but bypasses the handler layer, so anything the handlers compute
// server-side (Lead.Score/Classification, auto-assignment) is just given a
// plausible static value here instead.
func seedDemoData(db *gorm.DB) {
	var companyCount int64
	db.Model(&models.Company{}).Count(&companyCount)
	if companyCount > 0 {
		return
	}

	// Staff accounts — beyond the single seedAdmin Admin, so assignee
	// dropdowns/filters have real people spread across roles to pick from.
	staff := []models.User{
		{FirstName: "Nina", LastName: "Sales Rep", Email: "sales.rep@igeargeek.com", Role: models.RoleSalesRep},
		{FirstName: "Somsak", LastName: "Sales Manager", Email: "sales.manager@igeargeek.com", Role: models.RoleSalesManager},
		{FirstName: "Ploy", LastName: "Marketing", Email: "marketing@igeargeek.com", Role: models.RoleMarketing},
	}
	hash, err := utils.HashPassword(demoPassword)
	if err != nil {
		log.Printf("seedDemoData: failed to hash password, skipping: %v", err)
		return
	}
	for i := range staff {
		staff[i].PasswordHash = hash
		staff[i].IsActive = true
		if err := db.Create(&staff[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed staff user %s: %v", staff[i].Email, err)
			return
		}
	}
	salesRepID, salesManagerID, marketingID := staff[0].ID, staff[1].ID, staff[2].ID
	log.Printf("Seeded %d demo staff users (password: %s)", len(staff), demoPassword)

	companies := []models.Company{
		{Name: "Siam Tech Solutions", Industry: "Technology", Size: "51-200", RevenueSize: "5M - 20M THB", Website: "https://siamtech.co.th", Status: models.StatusActive},
		{Name: "Blue Ocean Retail", Industry: "Retail", Size: "11-50", RevenueSize: "1M - 5M THB", Website: "https://blueocean.co.th", Status: models.StatusActive},
		{Name: "Golden Grain Manufacturing", Industry: "Manufacturing", Size: "201-500", RevenueSize: "20M - 100M THB", Website: "https://goldengrain.co.th", Status: models.StatusActive},
	}
	for i := range companies {
		if err := db.Create(&companies[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed company %s: %v", companies[i].Name, err)
			return
		}
	}
	siamTech, blueOcean, goldenGrain := companies[0], companies[1], companies[2]

	contacts := []models.Contact{
		{CompanyID: siamTech.ID, Name: "Kittipong Wattana", Email: "kittipong@siamtech.co.th", Phone: "081-111-2222", RoleTitle: "CEO", Status: models.StatusActive},
		{CompanyID: siamTech.ID, Name: "Anong Srisuwan", Email: "anong@siamtech.co.th", Phone: "081-111-3333", RoleTitle: "Manager", Status: models.StatusActive},
		{CompanyID: blueOcean.ID, Name: "Suda Boonmee", Email: "suda@blueocean.co.th", Phone: "082-222-4444", RoleTitle: "Owner", Status: models.StatusActive},
		{CompanyID: blueOcean.ID, Name: "Chai Ratanakorn", Email: "chai@blueocean.co.th", Phone: "082-222-5555", RoleTitle: "Manager", Status: models.StatusActive},
		{CompanyID: goldenGrain.ID, Name: "Piya Chaiyasit", Email: "piya@goldengrain.co.th", Phone: "083-333-6666", RoleTitle: "Director", Status: models.StatusActive},
		{CompanyID: goldenGrain.ID, Name: "Malee Thongdee", Email: "malee@goldengrain.co.th", Phone: "083-333-7777", RoleTitle: "Staff", Status: models.StatusActive},
	}
	for i := range contacts {
		if err := db.Create(&contacts[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed contact %s: %v", contacts[i].Name, err)
			return
		}
	}
	suda, chai, piya, malee := contacts[2], contacts[3], contacts[4], contacts[5]

	now := time.Now()

	// Prospects — Marketing's funnel, spanning every status incl. one already
	// Converted (with a real Lead behind it, same as a live Convert would leave).
	prospects := []models.Prospect{
		{Name: "Napat Wongsawat", Email: "napat@example.com", Phone: "084-100-1001", Source: "Social Media", Status: models.ProspectStatusNew, AssignedTo: &marketingID},
		{Name: "Sirikan Phromsri", CompanyID: uintPtr(blueOcean.ID), Email: "sirikan@blueocean.co.th", Phone: "084-100-1002", Source: "LINE OA", Status: models.ProspectStatusEngaging, AssignedTo: &marketingID},
		{Name: "Teerapat Suksawat", Email: "teerapat@example.com", Phone: "084-100-1003", Source: "Email Campaign", Status: models.ProspectStatusNurturing, AssignedTo: &marketingID},
		{Name: "Kanya Intharak", Email: "kanya@example.com", Phone: "084-100-1004", Source: "Cold Outreach", Status: models.ProspectStatusDisqualified, AssignedTo: &marketingID},
		{Name: "Worapoj Chaisri", CompanyID: uintPtr(goldenGrain.ID), Email: "worapoj@goldengrain.co.th", Phone: "084-100-1005", Source: "Content/SEO", Status: models.ProspectStatusNew, AssignedTo: &marketingID},
		{Name: "Pattarapong Aksorn", CompanyID: uintPtr(siamTech.ID), Email: "pattarapong@siamtech.co.th", Phone: "084-100-1006", Source: "Marketing Campaign", Status: models.ProspectStatusConverted, AssignedTo: &marketingID},
	}
	for i := range prospects {
		if err := db.Create(&prospects[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed prospect %s: %v", prospects[i].Name, err)
			return
		}
	}
	engagingProspect, nurturingProspect, convertedProspect := prospects[1], prospects[2], prospects[5]

	// Leads — a few created directly, plus the one behind convertedProspect
	// above (ProspectID set, mirroring what POST /prospects/:id/convert does).
	leads := []models.Lead{
		{Name: "Nattaya Charoen", CompanyID: uintPtr(blueOcean.ID), Email: "nattaya@blueocean.co.th", Phone: "085-200-2001", Source: models.LeadSourceReferral, Status: models.LeadStatusNew, AssignedTo: &salesRepID},
		{Name: "Somchai Peerapong", CompanyID: uintPtr(goldenGrain.ID), Email: "somchai@goldengrain.co.th", Phone: "085-200-2002", Source: models.LeadSourceWebsite, Status: models.LeadStatusContacted, AssignedTo: &salesRepID},
		{Name: "Ratree Silpakit", CompanyID: uintPtr(siamTech.ID), Email: "ratree@siamtech.co.th", Phone: "085-200-2003", Source: models.LeadSourceEvent, Status: models.LeadStatusQualified, AssignedTo: &salesManagerID, Score: 40, Classification: "mql"},
		{Name: "Pichai Boonrod", Email: "pichai@example.com", Phone: "085-200-2004", Source: models.LeadSourceAds, Status: models.LeadStatusDisqualified, AssignedTo: &salesRepID},
		{Name: convertedProspect.Name, CompanyID: uintPtr(siamTech.ID), Email: convertedProspect.Email, Phone: convertedProspect.Phone, Source: models.LeadSource(convertedProspect.Source), Status: models.LeadStatusNew, AssignedTo: &marketingID, ProspectID: uintPtr(convertedProspect.ID)},
	}
	for i := range leads {
		if err := db.Create(&leads[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed lead %s: %v", leads[i].Name, err)
			return
		}
	}
	// Close the loop: mark the source Prospect converted, same as the real
	// Convert transaction does.
	if err := db.Model(&convertedProspect).Update("converted_lead_id", leads[4].ID).Error; err != nil {
		log.Printf("seedDemoData: failed to link converted prospect to its lead: %v", err)
	}

	// Deals — one per pipeline stage, so the Kanban board isn't empty in any column.
	dealSpecs := []struct {
		title   string
		company models.Company
		contact models.Contact
		stage   models.DealStage
		value   float64
		status  models.DealStatus
		lost    *models.LostReason
	}{
		{"Siam Tech - ERP Rollout", siamTech, contacts[0], models.DealStageLead, 850000, models.DealStatusOpen, nil},
		{"Blue Ocean - POS Upgrade", blueOcean, suda, models.DealStageQualified, 320000, models.DealStatusOpen, nil},
		{"Golden Grain - Factory Automation", goldenGrain, piya, models.DealStageProposalSent, 2400000, models.DealStatusOpen, nil},
		{"Siam Tech - Cloud Migration", siamTech, contacts[1], models.DealStageNegotiation, 1100000, models.DealStatusOpen, nil},
		{"Blue Ocean - Loyalty App", blueOcean, chai, models.DealStageWon, 480000, models.DealStatusWon, nil},
		{"Golden Grain - Legacy System Replace", goldenGrain, malee, models.DealStageLost, 900000, models.DealStatusLost, func() *models.LostReason { r := models.LostReasonPrice; return &r }()},
	}
	deals := make([]models.Deal, 0, len(dealSpecs))
	for _, spec := range dealSpecs {
		prob := models.StageDefaultProbability(spec.stage)
		deal := models.Deal{
			CompanyID: spec.company.ID, ContactID: spec.contact.ID, Title: spec.title, Value: spec.value,
			Stage: spec.stage, Status: spec.status, AssignedTo: &salesRepID, Channel: models.LeadSourceReferral,
			Probability: &prob, LostReason: spec.lost,
		}
		if err := db.Create(&deal).Error; err != nil {
			log.Printf("seedDemoData: failed to seed deal %s: %v", spec.title, err)
			return
		}
		deals = append(deals, deal)
	}

	// Tasks — spread across a few different related entities (Deal/Contact/
	// Company/Prospect), matching the polymorphic related_type/related_id shape.
	tasks := []models.Task{
		{RelatedType: models.RelatedTypeDeal, RelatedID: deals[0].ID, Title: "Send ERP proposal draft", DueDate: now.Add(3 * 24 * time.Hour), Priority: models.TaskPriorityMedium, AssignedTo: &salesRepID},
		{RelatedType: models.RelatedTypeContact, RelatedID: suda.ID, Title: "Follow up on POS quote", DueDate: now.Add(2 * 24 * time.Hour), Priority: models.TaskPriorityHigh, AssignedTo: &salesRepID},
		{RelatedType: models.RelatedTypeCompany, RelatedID: goldenGrain.ID, Title: "Schedule factory site visit", DueDate: now.Add(5 * 24 * time.Hour), Priority: models.TaskPriorityMedium, AssignedTo: &salesManagerID},
		{RelatedType: models.RelatedTypeProspect, RelatedID: engagingProspect.ID, Title: "Send LINE OA welcome message", DueDate: now.Add(1 * 24 * time.Hour), Priority: models.TaskPriorityHigh, AssignedTo: &marketingID},
		{RelatedType: models.RelatedTypeProspect, RelatedID: nurturingProspect.ID, Title: "Share case study PDF", DueDate: now.Add(-2 * 24 * time.Hour), Status: models.TaskStatusDone, Priority: models.TaskPriorityLow, AssignedTo: &marketingID},
		{RelatedType: models.RelatedTypeDeal, RelatedID: deals[3].ID, Title: "Prepare contract draft", DueDate: now.Add(4 * 24 * time.Hour), Priority: models.TaskPriorityHigh, AssignedTo: &salesRepID},
	}
	for i := range tasks {
		if tasks[i].Priority == "" {
			tasks[i].Priority = models.TaskPriorityMedium
		}
		if err := db.Create(&tasks[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed task %s: %v", tasks[i].Title, err)
			return
		}
	}

	projects := []models.Project{
		{CompanyID: blueOcean.ID, DealID: uintPtr(deals[4].ID), Name: "Loyalty App Rollout", Status: models.ProjectStatusInProgress, StartDate: now.Add(-30 * 24 * time.Hour), TargetEndDate: timePtr(now.Add(60 * 24 * time.Hour))},
		{CompanyID: siamTech.ID, Name: "ERP Implementation Phase 1", Status: models.ProjectStatusNotStarted, StartDate: now.Add(14 * 24 * time.Hour)},
	}
	for i := range projects {
		if err := db.Create(&projects[i]).Error; err != nil {
			log.Printf("seedDemoData: failed to seed project %s: %v", projects[i].Name, err)
			return
		}
	}

	log.Printf("Seeded demo data: %d companies, %d contacts, %d prospects, %d leads, %d deals, %d tasks, %d projects",
		len(companies), len(contacts), len(prospects), len(leads), len(deals), len(tasks), len(projects))
}

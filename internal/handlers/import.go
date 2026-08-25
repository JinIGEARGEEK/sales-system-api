package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const maxImportSize = 10 * 1024 * 1024

// maxImportRows bounds a single import request — without it, a many-
// thousand-row CSV meant a same number of sequential SELECT+write round
// trips to Postgres inside one request/goroutine (no batching, no cap). A
// file over this needs splitting into multiple imports rather than one
// request that can hold a connection/goroutine open indefinitely.
const maxImportRows = 5000

var (
	errImportFileTooLarge      = errors.New("import file too large")
	errImportUnsupportedFormat = errors.New("unsupported import file format")
	errImportTooManyRows       = fmt.Errorf("import file exceeds the %d row limit", maxImportRows)
)

type ImportHandler struct {
	DB *gorm.DB
}

func NewImportHandler(db *gorm.DB) *ImportHandler {
	return &ImportHandler{DB: db}
}

type importError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type importResult struct {
	Created int           `json:"created"`
	Updated int           `json:"updated"`
	Skipped int           `json:"skipped"`
	Errors  []importError `json:"errors"`
}

// openImportFile validates and reads the multipart `file` field. CSV only for
// this v1 — XLS/XLSX parsing is a follow-up, not implemented here.
func openImportFile(c *fiber.Ctx) ([][]string, error) {
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("missing file")
	}
	if fh.Size > maxImportSize {
		return nil, errImportFileTooLarge
	}
	if !strings.HasSuffix(strings.ToLower(fh.Filename), ".csv") {
		return nil, errImportUnsupportedFormat
	}

	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(rows) > 0 {
		rows = rows[1:]
	}
	if len(rows) > maxImportRows {
		return nil, errImportTooManyRows
	}
	return rows, nil
}

func respondImportFileError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, errImportFileTooLarge):
		return utils.ErrorResponse(c, fiber.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "File exceeds 10MB limit")
	case errors.Is(err, errImportUnsupportedFormat):
		return utils.BadRequest(c, "Only CSV files are supported")
	case errors.Is(err, errImportTooManyRows):
		return utils.BadRequest(c, err.Error())
	default:
		return utils.BadRequest(c, "Invalid or missing file")
	}
}

// normalizeName lowercases and trims a company name for case/whitespace
// insensitive fallback matching.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// companyIndex is an in-memory lookup built once per import (two SELECTs
// total, rather than the one-SELECT-per-row findExistingCompany used to run)
// so ImportCompanies can dedupe every row against both prior-existing
// companies and companies just created/updated earlier in the same file.
// Keeps findExistingCompany's exact semantics: a domain match wins when the
// row has a website; a company found there is entered into byName too so a
// later row matching only by name still finds it.
type companyIndex struct {
	byDomain map[string]*models.Company
	byName   map[string]*models.Company
}

func newCompanyIndex(db *gorm.DB, names, websites []string) (*companyIndex, error) {
	idx := &companyIndex{byDomain: map[string]*models.Company{}, byName: map[string]*models.Company{}}

	domainSet := map[string]bool{}
	for _, w := range websites {
		if d := utils.ExtractDomain(w); d != "" {
			domainSet[d] = true
		}
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		if norm := normalizeName(n); norm != "" {
			nameSet[norm] = true
		}
	}

	add := func(company models.Company) {
		c := company
		idx.byName[normalizeName(c.Name)] = &c
		if c.Domain != "" {
			idx.byDomain[c.Domain] = &c
		}
	}

	if len(domainSet) > 0 {
		domains := make([]string, 0, len(domainSet))
		for d := range domainSet {
			domains = append(domains, d)
		}
		var companies []models.Company
		if err := db.Where("domain IN ?", domains).Find(&companies).Error; err != nil {
			return nil, err
		}
		for _, comp := range companies {
			add(comp)
		}
	}
	if len(nameSet) > 0 {
		names := make([]string, 0, len(nameSet))
		for n := range nameSet {
			names = append(names, n)
		}
		var companies []models.Company
		if err := db.Where("LOWER(TRIM(name)) IN ?", names).Find(&companies).Error; err != nil {
			return nil, err
		}
		for _, comp := range companies {
			add(comp)
		}
	}
	return idx, nil
}

// lookup mirrors findExistingCompany's original fallback order: a domain
// match wins when the row has a website; otherwise (or when no company has
// that domain yet) fall back to the normalized-name match.
func (idx *companyIndex) lookup(name, website string) *models.Company {
	if domain := utils.ExtractDomain(website); domain != "" {
		if c, ok := idx.byDomain[domain]; ok {
			return c
		}
	}
	if c, ok := idx.byName[normalizeName(name)]; ok {
		return c
	}
	return nil
}

func (idx *companyIndex) put(company *models.Company) {
	idx.byName[normalizeName(company.Name)] = company
	if company.Domain != "" {
		idx.byDomain[company.Domain] = company
	}
}

// ImportCompanies — POST /companies/import. Expects columns: name,industry,size,website.
// Dedupes primarily by normalized website domain, falling back to a
// case-insensitive/whitespace-trimmed name match when either side has no
// website, per FR-CRM-014.
//
// Runs as one transaction and pre-loads every candidate match up front (two
// SELECTs total) instead of a SELECT-then-write per row — a large file used
// to mean thousands of sequential round trips to Postgres inside a single
// request, with no all-or-nothing guarantee if it failed partway through.
func (h *ImportHandler) ImportCompanies(c *fiber.Ctx) error {
	rows, err := openImportFile(c)
	if err != nil {
		return respondImportFileError(c, err)
	}

	type parsedRow struct {
		rowNum                    int
		name, industry, size, web string
	}
	parsed := make([]parsedRow, 0, len(rows))
	result := importResult{Errors: []importError{}}
	for i, row := range rows {
		rowNum := i + 2
		if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "name is required"})
			result.Skipped++
			continue
		}
		pr := parsedRow{rowNum: rowNum, name: strings.TrimSpace(row[0])}
		if len(row) > 1 {
			pr.industry = strings.TrimSpace(row[1])
		}
		if len(row) > 2 {
			pr.size = strings.TrimSpace(row[2])
		}
		if len(row) > 3 {
			pr.web = strings.TrimSpace(row[3])
		}
		parsed = append(parsed, pr)
	}

	names := make([]string, len(parsed))
	websites := make([]string, len(parsed))
	for i, pr := range parsed {
		names[i], websites[i] = pr.name, pr.web
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		idx, err := newCompanyIndex(tx, names, websites)
		if err != nil {
			return err
		}

		for _, pr := range parsed {
			if existing := idx.lookup(pr.name, pr.web); existing != nil {
				existing.Industry, existing.Size, existing.Website = pr.industry, pr.size, pr.web
				existing.Domain = utils.ExtractDomain(pr.web)
				if err := tx.Save(existing).Error; err != nil {
					result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to update"})
					result.Skipped++
					continue
				}
				idx.put(existing)
				result.Updated++
				continue
			}

			company := models.Company{Name: pr.name, Industry: pr.industry, Size: pr.size, Website: pr.web, Domain: utils.ExtractDomain(pr.web), Status: models.StatusActive}
			if err := tx.Create(&company).Error; err != nil {
				result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to create"})
				result.Skipped++
				continue
			}
			idx.put(&company)
			result.Created++
		}
		return nil
	})
	if err != nil {
		return utils.Internal(c, "Failed to import companies")
	}
	return utils.OK(c, result)
}

// ImportContacts — POST /contacts/import. Expects columns:
// company_id,name,email,phone,role_title. Dedupes by email per FR-CRM-014.
//
// Same batching/transaction treatment as ImportCompanies: one preloaded
// email→Contact map (one SELECT) instead of a SELECT-then-write per row, the
// whole import committed atomically.
func (h *ImportHandler) ImportContacts(c *fiber.Ctx) error {
	rows, err := openImportFile(c)
	if err != nil {
		return respondImportFileError(c, err)
	}

	type parsedRow struct {
		rowNum                        int
		companyID                     uint
		name, email, phone, roleTitle string
	}
	parsed := make([]parsedRow, 0, len(rows))
	result := importResult{Errors: []importError{}}
	for i, row := range rows {
		rowNum := i + 2
		if len(row) < 2 || strings.TrimSpace(row[0]) == "" || strings.TrimSpace(row[1]) == "" {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "company_id and name are required"})
			result.Skipped++
			continue
		}
		var companyID uint
		if _, err := fmt.Sscanf(strings.TrimSpace(row[0]), "%d", &companyID); err != nil {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "invalid company_id"})
			result.Skipped++
			continue
		}
		pr := parsedRow{rowNum: rowNum, companyID: companyID, name: strings.TrimSpace(row[1])}
		if len(row) > 2 {
			pr.email = strings.TrimSpace(row[2])
		}
		if len(row) > 3 {
			pr.phone = strings.TrimSpace(row[3])
		}
		if len(row) > 4 {
			pr.roleTitle = strings.TrimSpace(row[4])
		}
		parsed = append(parsed, pr)
	}

	emailSet := map[string]bool{}
	for _, pr := range parsed {
		if pr.email != "" {
			emailSet[pr.email] = true
		}
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		byEmail := map[string]*models.Contact{}
		if len(emailSet) > 0 {
			emails := make([]string, 0, len(emailSet))
			for e := range emailSet {
				emails = append(emails, e)
			}
			var contacts []models.Contact
			if err := tx.Where("email IN ?", emails).Find(&contacts).Error; err != nil {
				return err
			}
			for _, ct := range contacts {
				byEmail[ct.Email] = &ct
			}
		}

		for _, pr := range parsed {
			if pr.email != "" {
				if existing, ok := byEmail[pr.email]; ok {
					existing.Name, existing.Phone, existing.RoleTitle, existing.CompanyID = pr.name, pr.phone, pr.roleTitle, pr.companyID
					if err := tx.Save(existing).Error; err != nil {
						result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to update"})
						result.Skipped++
						continue
					}
					result.Updated++
					continue
				}
			}

			contact := models.Contact{
				CompanyID: pr.companyID, Name: pr.name, Email: pr.email, Phone: pr.phone, RoleTitle: pr.roleTitle,
				Status: models.StatusActive,
			}
			if err := tx.Create(&contact).Error; err != nil {
				result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to create"})
				result.Skipped++
				continue
			}
			if pr.email != "" {
				byEmail[pr.email] = &contact
			}
			result.Created++
		}
		return nil
	})
	if err != nil {
		return utils.Internal(c, "Failed to import contacts")
	}
	return utils.OK(c, result)
}

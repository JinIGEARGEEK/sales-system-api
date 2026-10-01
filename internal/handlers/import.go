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

// importRowSavepoint is the savepoint importRow sets before each row.
const importRowSavepoint = "import_row"

// importRow runs one row's writes under a savepoint. In Postgres a failed
// statement aborts the whole transaction, so without one a single bad row
// made every later row (and the commit) fail too. A row's own failure is
// rolled back to the savepoint and returned as rowErr, for the caller to
// report and skip; err is a savepoint failure that should abort the import.
func importRow(tx *gorm.DB, write func() error) (rowErr, err error) {
	if err := tx.SavePoint(importRowSavepoint).Error; err != nil {
		return nil, err
	}
	if rowErr := write(); rowErr != nil {
		if err := tx.RollbackTo(importRowSavepoint).Error; err != nil {
			return nil, err
		}
		return rowErr, nil
	}
	return nil, tx.Exec("RELEASE SAVEPOINT " + importRowSavepoint).Error
}

// hasNUL reports whether any cell holds a NUL byte, which Postgres text
// rejects. Such a row is skipped at parse time: in the batched lookup
// queries it would fail the whole import, not just its own row.
func hasNUL(row []string) bool {
	for _, cell := range row {
		if strings.ContainsRune(cell, 0) {
			return true
		}
	}
	return false
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

// ImportCompanies godoc
// @Summary Bulk-import companies from CSV
// @Description Uploads a CSV file (header row skipped, up to 10MB / 5000 data rows) with columns name,industry,size,website — name is required per row. Dedupes primarily by normalized website domain, falling back to a case-insensitive/whitespace-trimmed name match when either side has no website (FR-CRM-014): a match updates the existing Company, otherwise a new one is created. Runs in one transaction with a savepoint per row, so a row that fails to save is rolled back, reported in errors and skipped while the rest are imported.
// @Tags companies
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "CSV file: name,industry,size,website"
// @Success 200 {object} importResult "created/updated/skipped counts plus a per-row error list"
// @Failure 400 {object} map[string]interface{} "Missing/invalid file, unsupported format (non-.csv), or row limit exceeded"
// @Router /companies/import [post]
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
		if hasNUL(row) {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "row contains a NUL character"})
			result.Skipped++
			continue
		}
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
			if found := idx.lookup(pr.name, pr.web); found != nil {
				// A copy, so a failed save leaves the index as it was.
				existing := *found
				existing.Industry, existing.Size, existing.Website = pr.industry, pr.size, pr.web
				existing.Domain = utils.ExtractDomain(pr.web)
				rowErr, err := importRow(tx, func() error { return tx.Save(&existing).Error })
				if err != nil {
					return err
				}
				if rowErr != nil {
					result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to update"})
					result.Skipped++
					continue
				}
				// Written back, so every index entry for it sees the save.
				*found = existing
				idx.put(found)
				result.Updated++
				continue
			}

			company := models.Company{Name: pr.name, Industry: pr.industry, Size: pr.size, Website: pr.web, Domain: utils.ExtractDomain(pr.web), Status: models.StatusActive}
			rowErr, err := importRow(tx, func() error { return tx.Create(&company).Error })
			if err != nil {
				return err
			}
			if rowErr != nil {
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

// ImportContacts godoc
// @Summary Bulk-import contacts from CSV
// @Description Uploads a CSV file (header row skipped, up to 10MB / 5000 data rows) with columns company_id,name,email,phone,role_title — company_id and name are required per row, and every company_id must be an existing Company (422 before anything is imported). Dedupes by email per FR-CRM-014, case-insensitively and within the row's own Company: a match there is updated (name always; phone/role_title only when the CSV cell is non-empty), otherwise a new Contact is created — a Contact is never moved to another Company. Same savepoint-per-row treatment as ImportCompanies.
// @Tags contacts
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "CSV file: company_id,name,email,phone,role_title"
// @Success 200 {object} importResult "created/updated/skipped counts plus a per-row error list"
// @Failure 400 {object} map[string]interface{} "Missing/invalid file, unsupported format (non-.csv), or row limit exceeded"
// @Failure 422 {object} map[string]interface{} "A company_id that doesn't name an existing Company"
// @Router /contacts/import [post]
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
		if hasNUL(row) {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "row contains a NUL character"})
			result.Skipped++
			continue
		}
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

	// Every company_id must exist before anything is written: a missing
	// one would otherwise leave Contacts pointing at no Company.
	companySet := map[uint]bool{}
	for _, pr := range parsed {
		companySet[pr.companyID] = true
	}
	companyIDs := make([]uint, 0, len(companySet))
	for id := range companySet {
		companyIDs = append(companyIDs, id)
	}
	if len(companyIDs) > 0 {
		var found []uint
		if err := h.DB.Model(&models.Company{}).Where("id IN ?", companyIDs).Pluck("id", &found).Error; err != nil {
			return utils.Internal(c, "Failed to import contacts")
		}
		for _, id := range found {
			delete(companySet, id)
		}
		if len(companySet) > 0 {
			missing := make([]string, 0, len(companySet))
			for _, pr := range parsed {
				if companySet[pr.companyID] {
					missing = append(missing, fmt.Sprintf("%d (row %d)", pr.companyID, pr.rowNum))
				}
			}
			return utils.ValidationError(c, "company_id not found: "+strings.Join(missing, ", "),
				map[string][]string{"company_id": {"not_found"}})
		}
	}

	// Contacts are matched on (company_id, lower(email)): the same address
	// under another Company is a different Contact, not one to move here.
	type contactKey struct {
		companyID uint
		email     string
	}
	emailSet := map[string]bool{}
	for _, pr := range parsed {
		if e := utils.NormalizeEmail(pr.email); e != "" {
			emailSet[e] = true
		}
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		byKey := map[contactKey]*models.Contact{}
		if len(emailSet) > 0 {
			emails := make([]string, 0, len(emailSet))
			for e := range emailSet {
				emails = append(emails, e)
			}
			var contacts []models.Contact
			// Newest first, so with several matches the oldest is written
			// last and kept.
			if err := tx.Where("company_id IN ? AND LOWER(TRIM(email)) IN ?", companyIDs, emails).
				Order("id DESC").Find(&contacts).Error; err != nil {
				return err
			}
			for i := range contacts {
				ct := &contacts[i]
				byKey[contactKey{ct.CompanyID, utils.NormalizeEmail(ct.Email)}] = ct
			}
		}

		for _, pr := range parsed {
			key := contactKey{pr.companyID, utils.NormalizeEmail(pr.email)}
			if found, ok := byKey[key]; ok && key.email != "" {
				// A copy, so a failed save leaves the map as it was. An empty
				// phone/role_title cell keeps the stored value.
				existing := *found
				existing.Name = pr.name
				if pr.phone != "" {
					existing.Phone = pr.phone
				}
				if pr.roleTitle != "" {
					existing.RoleTitle = pr.roleTitle
				}
				rowErr, err := importRow(tx, func() error { return tx.Save(&existing).Error })
				if err != nil {
					return err
				}
				if rowErr != nil {
					result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to update"})
					result.Skipped++
					continue
				}
				*found = existing
				result.Updated++
				continue
			}

			contact := models.Contact{
				CompanyID: pr.companyID, Name: pr.name, Email: pr.email, Phone: pr.phone, RoleTitle: pr.roleTitle,
				Status: models.StatusActive,
			}
			rowErr, err := importRow(tx, func() error { return tx.Create(&contact).Error })
			if err != nil {
				return err
			}
			if rowErr != nil {
				result.Errors = append(result.Errors, importError{Row: pr.rowNum, Message: "failed to create"})
				result.Skipped++
				continue
			}
			if key.email != "" {
				byKey[key] = &contact
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

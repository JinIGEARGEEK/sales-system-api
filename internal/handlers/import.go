package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const maxImportSize = 10 * 1024 * 1024

// maxImportRows bounds a single import request, since every row is a write
// round trip inside one transaction. A larger file is split into several
// imports.
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

func newImportResult() importResult { return importResult{Errors: []importError{}} }

// skip records row as skipped with msg.
func (r *importResult) skip(row int, msg string) {
	r.Errors = append(r.Errors, importError{Row: row, Message: msg})
	r.Skipped++
}

// cell returns row[i] trimmed, or "" when the row is shorter.
func cell(row []string, i int) string {
	if i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// importRowNum is the 1-based CSV line of data row i (the header is line 1).
func importRowNum(i int) int { return i + 2 }

// importSaveRow runs write under importRow's savepoint and records the
// outcome: false (and a skip with failMsg) when the row failed. err aborts
// the import.
func importSaveRow(tx *gorm.DB, result *importResult, rowNum int, failMsg string, write func() error) (ok bool, err error) {
	rowErr, err := importRow(tx, write)
	if err != nil {
		return false, err
	}
	if rowErr != nil {
		result.skip(rowNum, failMsg)
		return false, nil
	}
	return true, nil
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
// statement aborts the whole transaction, so the savepoint is what keeps one
// bad row from failing every later row and the commit. A row's own failure is
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

// companyIndex is an in-memory lookup built once per import (two SELECTs),
// so ImportCompanies can match every row against both existing companies
// and ones created or updated earlier in the same file.
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

	load := func(where string, set map[string]bool) error {
		if len(set) == 0 {
			return nil
		}
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		var companies []models.Company
		if err := db.Where(where, keys).Find(&companies).Error; err != nil {
			return err
		}
		for i := range companies {
			idx.put(&companies[i])
		}
		return nil
	}
	if err := load("domain IN ?", domainSet); err != nil {
		return nil, err
	}
	if err := load("LOWER(TRIM(name)) IN ?", nameSet); err != nil {
		return nil, err
	}
	return idx, nil
}

// lookup finds the Company a row updates: a domain match wins when the row
// has a website; otherwise a case-insensitive, trimmed name match, but only
// when the row or that Company has no website. Two websites on different
// domains are different companies, even under the same name.
func (idx *companyIndex) lookup(name, website string) *models.Company {
	domain := utils.ExtractDomain(website)
	if domain != "" {
		if c, ok := idx.byDomain[domain]; ok {
			return c
		}
	}
	c, ok := idx.byName[normalizeName(name)]
	if !ok || (domain != "" && c.Domain != "") {
		return nil
	}
	return c
}

func (idx *companyIndex) put(company *models.Company) {
	idx.byName[normalizeName(company.Name)] = company
	if company.Domain != "" {
		idx.byDomain[company.Domain] = company
	}
}

// companyImportRow is one parsed ImportCompanies row.
type companyImportRow struct {
	rowNum                    int
	name, industry, size, web string
}

// parseCompanyImportRows reads name,industry,size,website rows, recording
// rows without a name (or with a NUL) as skipped in result.
func parseCompanyImportRows(rows [][]string, result *importResult) []companyImportRow {
	parsed := make([]companyImportRow, 0, len(rows))
	for i, row := range rows {
		rowNum := importRowNum(i)
		if hasNUL(row) {
			result.skip(rowNum, "row contains a NUL character")
			continue
		}
		pr := companyImportRow{rowNum: rowNum, name: cell(row, 0), industry: cell(row, 1), size: cell(row, 2), web: cell(row, 3)}
		if pr.name == "" {
			result.skip(rowNum, "name is required")
			continue
		}
		parsed = append(parsed, pr)
	}
	return parsed
}

// ImportCompanies godoc
// @Summary Bulk-import companies from CSV
// @Description Uploads a CSV file (header row skipped, up to 10MB / 5000 data rows) with columns name,industry,size,website — name is required per row. A row matches an existing Company by normalized website domain, else by case-insensitive/whitespace-trimmed name when the row or that Company has no website (FR-CRM-014): a match is updated (name is kept; industry/size/website only when the CSV cell is non-empty), otherwise a new Company is created. Runs in one transaction with a savepoint per row, so a row that fails to save is rolled back, reported in errors and skipped while the rest are imported.
// @Tags companies
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "CSV file: name,industry,size,website"
// @Success 200 {object} importResult "created/updated/skipped counts plus a per-row error list"
// @Failure 400 {object} map[string]interface{} "Missing/invalid file, unsupported format (non-.csv), or row limit exceeded"
// @Failure 413 {object} map[string]interface{} "File over 10MB"
// @Router /companies/import [post]
func (h *ImportHandler) ImportCompanies(c *fiber.Ctx) error {
	rows, err := openImportFile(c)
	if err != nil {
		return respondImportFileError(c, err)
	}
	result := newImportResult()
	parsed := parseCompanyImportRows(rows, &result)

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
				// A copy, so a failed save leaves the index as it was. An
				// empty cell keeps the stored value.
				existing := *found
				if pr.industry != "" {
					existing.Industry = pr.industry
				}
				if pr.size != "" {
					existing.Size = pr.size
				}
				if pr.web != "" {
					existing.Website, existing.Domain = pr.web, utils.ExtractDomain(pr.web)
				}
				ok, err := importSaveRow(tx, &result, pr.rowNum, "failed to update", func() error { return tx.Save(&existing).Error })
				if err != nil {
					return err
				}
				if ok {
					// Written back, so every index entry for it sees the save.
					*found = existing
					idx.put(found)
					result.Updated++
				}
				continue
			}

			company := models.Company{Name: pr.name, Industry: pr.industry, Size: pr.size, Website: pr.web, Domain: utils.ExtractDomain(pr.web), Status: models.StatusActive}
			ok, err := importSaveRow(tx, &result, pr.rowNum, "failed to create", func() error { return tx.Create(&company).Error })
			if err != nil {
				return err
			}
			if ok {
				idx.put(&company)
				result.Created++
			}
		}
		return nil
	})
	if err != nil {
		return utils.Internal(c, "Failed to import companies")
	}
	return utils.OK(c, result)
}

// contactImportRow is one parsed ImportContacts row.
type contactImportRow struct {
	rowNum                        int
	companyID                     uint
	name, email, phone, roleTitle string
}

// parseContactImportRows reads company_id,name,email,phone,role_title rows,
// recording rows without a company_id/name, with a non-numeric company_id,
// or with a NUL as skipped in result.
func parseContactImportRows(rows [][]string, result *importResult) []contactImportRow {
	parsed := make([]contactImportRow, 0, len(rows))
	for i, row := range rows {
		rowNum := importRowNum(i)
		if hasNUL(row) {
			result.skip(rowNum, "row contains a NUL character")
			continue
		}
		rawCompanyID, name := cell(row, 0), cell(row, 1)
		if rawCompanyID == "" || name == "" {
			result.skip(rowNum, "company_id and name are required")
			continue
		}
		companyID, err := strconv.ParseUint(rawCompanyID, 10, 32)
		if err != nil {
			result.skip(rowNum, "invalid company_id")
			continue
		}
		parsed = append(parsed, contactImportRow{
			rowNum: rowNum, companyID: uint(companyID), name: name,
			email: cell(row, 2), phone: cell(row, 3), roleTitle: cell(row, 4),
		})
	}
	return parsed
}

// missingImportCompanies returns, for each row whose company_id names no
// live Company, "<id> (row <n>)", in row order; nil when all exist.
func missingImportCompanies(db *gorm.DB, parsed []contactImportRow, companyIDs []uint) ([]string, error) {
	if len(companyIDs) == 0 {
		return nil, nil
	}
	var found []uint
	if err := db.Model(&models.Company{}).Where("id IN ?", companyIDs).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	exists := make(map[uint]bool, len(found))
	for _, id := range found {
		exists[id] = true
	}
	var missing []string
	for _, pr := range parsed {
		if !exists[pr.companyID] {
			missing = append(missing, fmt.Sprintf("%d (row %d)", pr.companyID, pr.rowNum))
		}
	}
	return missing, nil
}

// contactImportKey is how ImportContacts matches a row to a Contact:
// (company_id, normalized email). The same address under another Company is
// a different Contact, never one to move.
type contactImportKey struct {
	companyID uint
	email     string
}

// loadImportContacts indexes the existing Contacts in companyIDs whose
// email is one of the rows'. With several matches the oldest wins.
func loadImportContacts(tx *gorm.DB, parsed []contactImportRow, companyIDs []uint) (map[contactImportKey]*models.Contact, error) {
	byKey := map[contactImportKey]*models.Contact{}
	emailSet := map[string]bool{}
	for _, pr := range parsed {
		if e := utils.NormalizeEmail(pr.email); e != "" {
			emailSet[e] = true
		}
	}
	if len(emailSet) == 0 {
		return byKey, nil
	}
	emails := make([]string, 0, len(emailSet))
	for e := range emailSet {
		emails = append(emails, e)
	}
	var contacts []models.Contact
	// Newest first, so the oldest is written last and kept.
	if err := tx.Where("company_id IN ? AND LOWER(TRIM(email)) IN ?", companyIDs, emails).
		Order("id DESC").Find(&contacts).Error; err != nil {
		return nil, err
	}
	for i := range contacts {
		ct := &contacts[i]
		byKey[contactImportKey{ct.CompanyID, utils.NormalizeEmail(ct.Email)}] = ct
	}
	return byKey, nil
}

// ImportContacts godoc
// @Summary Bulk-import contacts from CSV
// @Description Uploads a CSV file (header row skipped, up to 10MB / 5000 data rows) with columns company_id,name,email,phone,role_title — company_id (a whole number) and name are required per row, and every company_id must be an existing Company (422 before anything is imported). Dedupes by email per FR-CRM-014, case-insensitively and within the row's own Company: a match there is updated (name always; phone/role_title only when the CSV cell is non-empty), otherwise a new Contact is created — a Contact is never moved to another Company. Same savepoint-per-row treatment as ImportCompanies.
// @Tags contacts
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "CSV file: company_id,name,email,phone,role_title"
// @Success 200 {object} importResult "created/updated/skipped counts plus a per-row error list"
// @Failure 400 {object} map[string]interface{} "Missing/invalid file, unsupported format (non-.csv), or row limit exceeded"
// @Failure 413 {object} map[string]interface{} "File over 10MB"
// @Failure 422 {object} map[string]interface{} "A company_id that doesn't name an existing Company"
// @Router /contacts/import [post]
func (h *ImportHandler) ImportContacts(c *fiber.Ctx) error {
	rows, err := openImportFile(c)
	if err != nil {
		return respondImportFileError(c, err)
	}
	result := newImportResult()
	parsed := parseContactImportRows(rows, &result)

	companySet := map[uint]bool{}
	companyIDs := make([]uint, 0)
	for _, pr := range parsed {
		if !companySet[pr.companyID] {
			companySet[pr.companyID] = true
			companyIDs = append(companyIDs, pr.companyID)
		}
	}
	// Every company_id must exist before anything is written, since nothing
	// else stops a Contact pointing at no Company.
	missing, err := missingImportCompanies(h.DB, parsed, companyIDs)
	if err != nil {
		return utils.Internal(c, "Failed to import contacts")
	}
	if len(missing) > 0 {
		return utils.ValidationError(c, "company_id not found: "+strings.Join(missing, ", "),
			map[string][]string{"company_id": {"not_found"}})
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		byKey, err := loadImportContacts(tx, parsed, companyIDs)
		if err != nil {
			return err
		}
		for _, pr := range parsed {
			key := contactImportKey{pr.companyID, utils.NormalizeEmail(pr.email)}
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
				ok, err := importSaveRow(tx, &result, pr.rowNum, "failed to update", func() error { return tx.Save(&existing).Error })
				if err != nil {
					return err
				}
				if ok {
					*found = existing
					result.Updated++
				}
				continue
			}

			contact := models.Contact{
				CompanyID: pr.companyID, Name: pr.name, Email: pr.email, Phone: pr.phone, RoleTitle: pr.roleTitle,
				Status: models.StatusActive,
			}
			ok, err := importSaveRow(tx, &result, pr.rowNum, "failed to create", func() error { return tx.Create(&contact).Error })
			if err != nil {
				return err
			}
			if ok {
				if key.email != "" {
					byKey[key] = &contact
				}
				result.Created++
			}
		}
		return nil
	})
	if err != nil {
		return utils.Internal(c, "Failed to import contacts")
	}
	return utils.OK(c, result)
}

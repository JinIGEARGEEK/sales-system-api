package handlers

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"log"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// exportBatchSize bounds how many rows ExportHandler loads into memory at
// once. Each export streams the response via FindInBatches + a chunked HTTP
// body instead of Find()-ing the entire (unbounded, filterable) table into a
// single slice — a company/deal/contact table with a six-figure row count
// would otherwise risk an OOM or request-timeout on every export click.
const exportBatchSize = 500

// ExportHandler serves GET /{resource}/export CSV downloads — Admin/Sales
// Manager only (route-gated). Each export reuses its List handler's filter
// query params but skips pagination entirely so the file always covers the
// full (non-deleted) dataset, unlike the 200-row-capped List responses.
type ExportHandler struct {
	DB *gorm.DB
}

func NewExportHandler(db *gorm.DB) *ExportHandler {
	return &ExportHandler{DB: db}
}

// streamCSV writes the CSV header immediately and then streams rows to the
// client as they're produced by writeRows, instead of buffering the whole
// file in memory first — bounded memory regardless of how many rows the
// query matches. Once this starts, the 200 status and headers are effectively
// committed (fasthttp writes them to begin the chunked body) — a failure
// inside writeRows can only be logged and the stream cut short, it can no
// longer turn into an HTTP error response. exportStream below exists
// specifically to keep that window as small as possible.
func streamCSV(c *fiber.Ctx, filename string, header []string, writeRows func(w *csv.Writer) error) error {
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Context().SetBodyStreamWriter(func(bw *bufio.Writer) {
		w := csv.NewWriter(bw)
		if err := w.Write(header); err != nil {
			log.Printf("export %s: write header: %v", filename, err)
			return
		}
		if err := writeRows(w); err != nil {
			log.Printf("export %s: write rows: %v", filename, err)
			return
		}
		w.Flush()
		_ = bw.Flush()
	})
	return nil
}

// exportStream fetches query's first page synchronously — before calling
// streamCSV / committing to a 200 response — so a failure that would hit on
// the very first batch (bad connection, a malformed filter producing invalid
// SQL, a permissions error) surfaces as a normal 500 with an error body
// instead of a misleadingly-200'd response that silently truncates to just
// the CSV header. rowFn is called once per page (this first one, then each
// FindInBatches page after it) to render that page's rows.
//
// This can't close the window entirely: once the first page succeeds and
// streaming begins, a failure on some later page (row 50,001 of a 100,000-row
// export, say) can only be logged server-side and cut the stream short — the
// 200 and everything already flushed can't be un-sent. That's an inherent
// limit of any streamed HTTP response, not something to solve here; this
// function just makes sure the overwhelmingly common failure mode (the query
// never worked at all) still gets a proper error.
func exportStream[T any](c *fiber.Ctx, query *gorm.DB, filename string, header []string, rowFn func(w *csv.Writer, batch []T) error) error {
	var first []T
	if err := query.Session(&gorm.Session{}).Limit(exportBatchSize).Find(&first).Error; err != nil {
		log.Printf("export %s: %v", filename, err)
		return utils.Internal(c, "Failed to export data")
	}

	return streamCSV(c, filename, header, func(w *csv.Writer) error {
		if err := rowFn(w, first); err != nil {
			return err
		}
		if len(first) < exportBatchSize {
			return nil // fewer rows than one page — first Find already got everything
		}
		var rest []T
		return query.Session(&gorm.Session{}).Offset(exportBatchSize).
			FindInBatches(&rest, exportBatchSize, func(tx *gorm.DB, batchNum int) error {
				return rowFn(w, rest)
			}).Error
	})
}

func boolYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Companies — GET /companies/export. Filters mirror CompanyHandler.List.
func (h *ExportHandler) Companies(c *fiber.Ctx) error {
	query := applyCompanyFilters(h.DB.Model(&models.Company{}), c).Order("created_at DESC")

	header := []string{"Name", "Industry", "Size", "Website", "Tags", "Status", "Legal Name", "Address", "Tax ID", "Notes", "Created Date"}
	return exportStream(c, query, "companies.csv", header, func(w *csv.Writer, batch []models.Company) error {
		for _, co := range batch {
			if err := w.Write([]string{
				co.Name, co.Industry, co.Size, co.Website, joinTags(co.Tags), string(co.Status),
				derefStr(co.LegalName), derefStr(co.Address), derefStr(co.TaxID), co.Notes,
				co.CreatedAt.Format("2006-01-02"),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Contacts — GET /contacts/export. Filters mirror ContactHandler.List. Resolves
// company_id to the Company name, matching what the list page displays.
func (h *ExportHandler) Contacts(c *fiber.Ctx) error {
	query := applyContactFilters(h.DB.Model(&models.Contact{}), c).Order("created_at DESC")

	header := []string{"Name", "Company", "Email", "Phone", "Role/Title", "Tags", "Status", "Created Date"}
	return exportStream(c, query, "contacts.csv", header, func(w *csv.Writer, batch []models.Contact) error {
		companyNameByID := h.companyNamesFor(uniqueUintsFrom(batch, func(ct models.Contact) uint { return ct.CompanyID }))
		for _, ct := range batch {
			if err := w.Write([]string{
				ct.Name, companyNameByID[ct.CompanyID], ct.Email, ct.Phone, ct.RoleTitle,
				joinTags(ct.Tags), string(ct.Status), ct.CreatedAt.Format("2006-01-02"),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Deals — GET /deals/export. Filters mirror DealHandler.List. Resolves
// company_id/assigned_to to names.
func (h *ExportHandler) Deals(c *fiber.Ctx) error {
	query := applyDealFilters(h.DB.Model(&models.Deal{}), c).Order("created_at DESC")

	header := []string{
		"Title", "Company", "Value", "Stage", "Status", "Expected Close Date",
		"Assigned To", "Channel", "Business Unit", "Business Unit Item", "Tags", "Created Date",
	}
	return exportStream(c, query, "deals.csv", header, func(w *csv.Writer, batch []models.Deal) error {
		companyNameByID := h.companyNamesFor(uniqueUintsFrom(batch, func(d models.Deal) uint { return d.CompanyID }))
		userNameByID := h.userNamesFor(uniquePtrUintsFrom(batch, func(d models.Deal) *uint { return d.AssignedTo }))

		for _, d := range batch {
			assignedName := ""
			if d.AssignedTo != nil {
				assignedName = userNameByID[*d.AssignedTo]
			}
			businessUnit := ""
			if d.BusinessUnit != nil {
				businessUnit = string(*d.BusinessUnit)
			}
			if err := w.Write([]string{
				d.Title, companyNameByID[d.CompanyID], strconv.FormatFloat(d.Value, 'f', 2, 64),
				string(d.Stage), string(d.Status), derefStr(d.ExpectedCloseDate),
				assignedName, string(d.Channel), businessUnit, derefStr(d.BusinessUnitItem),
				joinTags(d.Tags), d.CreatedAt.Format("2006-01-02"),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Products — GET /products/export. Filters mirror ProductHandler.List.
func (h *ExportHandler) Products(c *fiber.Ctx) error {
	query := applyProductFilters(h.DB.Model(&models.Product{}), c).Order("created_at DESC")

	header := []string{"Name", "Category", "Description", "Active", "Created Date"}
	return exportStream(c, query, "products.csv", header, func(w *csv.Writer, batch []models.Product) error {
		for _, p := range batch {
			if err := w.Write([]string{
				p.Name, p.Category, p.Description, boolYesNo(p.IsActive), p.CreatedAt.Format("2006-01-02"),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Projects — GET /projects/export. Filters mirror ProjectHandler.List.
// Resolves company_id to the Company name.
func (h *ExportHandler) Projects(c *fiber.Ctx) error {
	query := applyProjectFilters(h.DB.Model(&models.Project{}), c).Order("created_at DESC")

	header := []string{"Name", "Company", "Status", "Start Date", "Target End Date", "Production Reference", "Notes", "Created Date"}
	return exportStream(c, query, "projects.csv", header, func(w *csv.Writer, batch []models.Project) error {
		companyNameByID := h.companyNamesFor(uniqueUintsFrom(batch, func(p models.Project) uint { return p.CompanyID }))
		for _, p := range batch {
			targetEnd := ""
			if p.TargetEndDate != nil {
				targetEnd = p.TargetEndDate.Format("2006-01-02")
			}
			if err := w.Write([]string{
				p.Name, companyNameByID[p.CompanyID], string(p.Status),
				p.StartDate.Format("2006-01-02"), targetEnd, derefStr(p.ProductionReference), p.Notes,
				p.CreatedAt.Format("2006-01-02"),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// companyNamesFor loads Company.Name for a set of ids — the join pattern
// ProjectHandler.List / ProductHandler.ListForCompany already use.
func (h *ExportHandler) companyNamesFor(ids []uint) map[uint]string {
	out := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	var companies []models.Company
	h.DB.Where("id IN ?", ids).Find(&companies)
	for _, co := range companies {
		out[co.ID] = co.Name
	}
	return out
}

// userNamesFor loads a display name ("First Last") per User id.
func (h *ExportHandler) userNamesFor(ids []uint) map[uint]string {
	out := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	var users []models.User
	h.DB.Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		out[u.ID] = fmt.Sprintf("%s %s", u.FirstName, u.LastName)
	}
	return out
}

// uniqueUintsFrom extracts a deduplicated id list from a batch via the given
// accessor, so a batch of e.g. 500 deals sharing a handful of companies
// doesn't send 500 duplicate ids into an IN (...) lookup.
func uniqueUintsFrom[T any](items []T, get func(T) uint) []uint {
	seen := make(map[uint]bool, len(items))
	out := make([]uint, 0, len(items))
	for _, item := range items {
		id := get(item)
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// uniquePtrUintsFrom is uniqueUintsFrom for a nullable id field (e.g. Deal.AssignedTo).
func uniquePtrUintsFrom[T any](items []T, get func(T) *uint) []uint {
	seen := make(map[uint]bool, len(items))
	out := make([]uint, 0, len(items))
	for _, item := range items {
		p := get(item)
		if p == nil || *p == 0 || seen[*p] {
			continue
		}
		seen[*p] = true
		out = append(out, *p)
	}
	return out
}

// joinTags renders a pq.StringArray as a single semicolon-separated CSV field.
func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += "; "
		}
		out += t
	}
	return out
}

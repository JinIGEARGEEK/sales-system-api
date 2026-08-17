package handlers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

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

// writeCSV renders rows into a CSV byte buffer with the given header.
func writeCSV(header []string, rows [][]string) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sendCSV writes the CSV bytes with the standard download headers.
func sendCSV(c *fiber.Ctx, filename string, body []byte) error {
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.Send(body)
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
	query := h.DB.Model(&models.Company{})
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("industry"); v != "" {
		query = query.Where("industry = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		query = query.Where("? = ANY(tags)", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("name ILIKE ?", "%"+v+"%")
	}

	var companies []models.Company
	if err := query.Order("created_at DESC").Find(&companies).Error; err != nil {
		return utils.Internal(c, "Failed to export companies")
	}

	header := []string{"Name", "Industry", "Size", "Website", "Tags", "Status", "Legal Name", "Address", "Tax ID", "Notes", "Created Date"}
	rows := make([][]string, 0, len(companies))
	for _, co := range companies {
		rows = append(rows, []string{
			co.Name, co.Industry, co.Size, co.Website, joinTags(co.Tags), string(co.Status),
			derefStr(co.LegalName), derefStr(co.Address), derefStr(co.TaxID), co.Notes,
			co.CreatedAt.Format("2006-01-02"),
		})
	}

	body, err := writeCSV(header, rows)
	if err != nil {
		return utils.Internal(c, "Failed to export companies")
	}
	return sendCSV(c, "companies.csv", body)
}

// Contacts — GET /contacts/export. Filters mirror ContactHandler.List. Resolves
// company_id to the Company name, matching what the list page displays.
func (h *ExportHandler) Contacts(c *fiber.Ctx) error {
	query := h.DB.Model(&models.Contact{})
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("tag"); v != "" {
		query = query.Where("? = ANY(tags)", v)
	}
	if v := c.Query("search"); v != "" {
		like := "%" + v + "%"
		query = query.Where("name ILIKE ? OR email ILIKE ?", like, like)
	}

	var contacts []models.Contact
	if err := query.Order("created_at DESC").Find(&contacts).Error; err != nil {
		return utils.Internal(c, "Failed to export contacts")
	}
	companyNameByID := h.companyNamesFor(idsFromContacts(contacts))

	header := []string{"Name", "Company", "Email", "Phone", "Role/Title", "Tags", "Status", "Created Date"}
	rows := make([][]string, 0, len(contacts))
	for _, ct := range contacts {
		rows = append(rows, []string{
			ct.Name, companyNameByID[ct.CompanyID], ct.Email, ct.Phone, ct.RoleTitle,
			joinTags(ct.Tags), string(ct.Status), ct.CreatedAt.Format("2006-01-02"),
		})
	}

	body, err := writeCSV(header, rows)
	if err != nil {
		return utils.Internal(c, "Failed to export contacts")
	}
	return sendCSV(c, "contacts.csv", body)
}

// Deals — GET /deals/export. Filters mirror DealHandler.List. Resolves
// company_id/assigned_to to names.
func (h *ExportHandler) Deals(c *fiber.Ctx) error {
	query := h.DB.Model(&models.Deal{})
	if v := c.Query("stage"); v != "" {
		query = query.Where("stage = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}
	if v := c.Query("assigned_to"); v != "" {
		query = query.Where("assigned_to = ?", v)
	}
	if v := c.Query("business_unit"); v != "" {
		query = query.Where("business_unit = ?", v)
	}
	if v := c.Query("channel"); v != "" {
		query = query.Where("channel = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("title ILIKE ?", "%"+v+"%")
	}

	var deals []models.Deal
	if err := query.Order("created_at DESC").Find(&deals).Error; err != nil {
		return utils.Internal(c, "Failed to export deals")
	}

	companyIDs := make([]uint, 0, len(deals))
	userIDs := make([]uint, 0, len(deals))
	for _, d := range deals {
		companyIDs = append(companyIDs, d.CompanyID)
		if d.AssignedTo != nil {
			userIDs = append(userIDs, *d.AssignedTo)
		}
	}
	companyNameByID := h.companyNamesFor(companyIDs)
	userNameByID := h.userNamesFor(userIDs)

	header := []string{
		"Title", "Company", "Value", "Stage", "Status", "Expected Close Date",
		"Assigned To", "Channel", "Business Unit", "Business Unit Item", "Tags", "Created Date",
	}
	rows := make([][]string, 0, len(deals))
	for _, d := range deals {
		assignedName := ""
		if d.AssignedTo != nil {
			assignedName = userNameByID[*d.AssignedTo]
		}
		businessUnit := ""
		if d.BusinessUnit != nil {
			businessUnit = string(*d.BusinessUnit)
		}
		rows = append(rows, []string{
			d.Title, companyNameByID[d.CompanyID], strconv.FormatFloat(d.Value, 'f', 2, 64),
			string(d.Stage), string(d.Status), derefStr(d.ExpectedCloseDate),
			assignedName, string(d.Channel), businessUnit, derefStr(d.BusinessUnitItem),
			joinTags(d.Tags), d.CreatedAt.Format("2006-01-02"),
		})
	}

	body, err := writeCSV(header, rows)
	if err != nil {
		return utils.Internal(c, "Failed to export deals")
	}
	return sendCSV(c, "deals.csv", body)
}

// Products — GET /products/export. Filters mirror ProductHandler.List.
func (h *ExportHandler) Products(c *fiber.Ctx) error {
	query := h.DB.Model(&models.Product{})
	if v := c.Query("category"); v != "" {
		query = query.Where("category = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("name ILIKE ?", "%"+v+"%")
	}

	var products []models.Product
	if err := query.Order("created_at DESC").Find(&products).Error; err != nil {
		return utils.Internal(c, "Failed to export products")
	}

	header := []string{"Name", "Category", "Description", "Active", "Created Date"}
	rows := make([][]string, 0, len(products))
	for _, p := range products {
		rows = append(rows, []string{
			p.Name, p.Category, p.Description, boolYesNo(p.IsActive), p.CreatedAt.Format("2006-01-02"),
		})
	}

	body, err := writeCSV(header, rows)
	if err != nil {
		return utils.Internal(c, "Failed to export products")
	}
	return sendCSV(c, "products.csv", body)
}

// Projects — GET /projects/export. Filters mirror ProjectHandler.List.
// Resolves company_id to the Company name.
func (h *ExportHandler) Projects(c *fiber.Ctx) error {
	query := h.DB.Model(&models.Project{})
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := c.Query("company_id"); v != "" {
		query = query.Where("company_id = ?", v)
	}

	var projects []models.Project
	if err := query.Order("created_at DESC").Find(&projects).Error; err != nil {
		return utils.Internal(c, "Failed to export projects")
	}

	companyIDs := make([]uint, 0, len(projects))
	for _, p := range projects {
		companyIDs = append(companyIDs, p.CompanyID)
	}
	companyNameByID := h.companyNamesFor(companyIDs)

	header := []string{"Name", "Company", "Status", "Start Date", "Target End Date", "Production Reference", "Notes", "Created Date"}
	rows := make([][]string, 0, len(projects))
	for _, p := range projects {
		targetEnd := ""
		if p.TargetEndDate != nil {
			targetEnd = p.TargetEndDate.Format("2006-01-02")
		}
		rows = append(rows, []string{
			p.Name, companyNameByID[p.CompanyID], string(p.Status),
			p.StartDate.Format("2006-01-02"), targetEnd, derefStr(p.ProductionReference), p.Notes,
			p.CreatedAt.Format("2006-01-02"),
		})
	}

	body, err := writeCSV(header, rows)
	if err != nil {
		return utils.Internal(c, "Failed to export projects")
	}
	return sendCSV(c, "projects.csv", body)
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

func idsFromContacts(contacts []models.Contact) []uint {
	ids := make([]uint, 0, len(contacts))
	for _, ct := range contacts {
		ids = append(ids, ct.CompanyID)
	}
	return ids
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

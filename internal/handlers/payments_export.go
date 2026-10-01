package handlers

import (
	"encoding/csv"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// paymentExportRow is one Payment joined to the names the CSV prints.
type paymentExportRow struct {
	ID                 uint
	PaidAt             time.Time
	DocumentNumber     *string
	DealID             uint
	DealTitle          string
	CompanyName        *string
	Amount             float64
	WhtAmount          float64
	Method             string
	InstallmentID      *uint
	InstallmentDueDate *time.Time
	Note               string
	CreatedByFirst     *string
	CreatedByLast      *string
}

// paymentsExportHeader is the Payments CSV's header row.
var paymentsExportHeader = []string{
	"Paid At", "Document Number", "Deal ID", "Deal", "Company", "Amount", "WHT Amount", "Total",
	"Method", "Installment ID", "Installment Due Date", "Note", "Created By",
}

// paymentsExportQuery builds GET /payments/export's query from its filters:
// date_from/date_to (inclusive server-local days on paid_at), deal_id,
// company_id (the Deal's Company) and method. Soft-deleted Payments (default
// scope) and Payments on soft-deleted Deals are left out. A bad filter
// writes the 422 itself and returns utils.ErrHandled.
func paymentsExportQuery(c *fiber.Ctx, db *gorm.DB) (*gorm.DB, error) {
	r, err := dateRangeQuery(c)
	if err != nil {
		_ = reportError(c, err, "Failed to export payments")
		return nil, utils.ErrHandled
	}
	query := db.Model(&models.Payment{}).
		Select(`payments.id, payments.paid_at, payments.document_number, payments.deal_id,
			deals.title AS deal_title, companies.name AS company_name,
			payments.amount, payments.wht_amount, payments.method, payments.installment_id,
			payment_installments.due_date AS installment_due_date, payments.note,
			users.first_name AS created_by_first, users.last_name AS created_by_last`).
		Joins("JOIN deals ON deals.id = payments.deal_id AND deals.deleted_at IS NULL").
		Joins("LEFT JOIN companies ON companies.id = deals.company_id").
		Joins("LEFT JOIN payment_installments ON payment_installments.id = payments.installment_id").
		Joins("LEFT JOIN users ON users.id = payments.created_by")
	query = r.Apply(query, "payments.paid_at")

	for _, f := range []struct{ name, column string }{
		{"deal_id", "payments.deal_id"}, {"company_id", "deals.company_id"},
	} {
		v := c.Query(f.name)
		if v == "" {
			continue
		}
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 {
			_ = utils.ValidationError(c, f.name+" is invalid", map[string][]string{f.name: {"must be a positive integer"}})
			return nil, utils.ErrHandled
		}
		query = query.Where(f.column+" = ?", id)
	}
	if v := c.Query("method"); v != "" {
		if !models.IsValidPaymentMethod(models.PaymentMethod(v)) {
			_ = utils.ValidationError(c, "method is invalid", map[string][]string{"method": {"invalid"}})
			return nil, utils.ErrHandled
		}
		query = query.Where("payments.method = ?", v)
	}
	return query.Order("payments.paid_at ASC, payments.id ASC"), nil
}

// Payments godoc
// @Summary Export payments as CSV (Admin/Sales Manager only)
// @Description CSV download of every non-deleted Payment on a non-deleted Deal, oldest paid_at first, for reconciling against FlowAccount. Columns: Paid At (YYYY-MM-DD, server-local), Document Number, Deal ID, Deal, Company, Amount (cash), WHT Amount, Total (amount + WHT), Method, Installment ID, Installment Due Date, Note, Created By. Filename payments-YYYYMMDD.csv (today, server-local). Admin/Sales Manager only. api-system-spec.md §7.5.
// @Tags export
// @Security BearerAuth
// @Produce text/csv
// @Param date_from query string false "paid_at on or after this day (YYYY-MM-DD, server-local)"
// @Param date_to query string false "paid_at on or before this day (YYYY-MM-DD, server-local, inclusive)"
// @Param deal_id query int false "Only this Deal's payments"
// @Param company_id query int false "Only payments on this Company's Deals"
// @Param method query string false "cash, transfer, card or other"
// @Success 200 {file} file "CSV export"
// @Failure 403 {object} map[string]interface{} "Not Admin/Sales Manager"
// @Failure 422 {object} map[string]interface{} "Invalid date_from/date_to (or date_to before date_from), deal_id, company_id or method"
// @Failure 500 {object} map[string]interface{} "Failed to export data"
// @Router /payments/export [get]
func (h *ExportHandler) Payments(c *fiber.Ctx) error {
	query, err := paymentsExportQuery(c, h.DB)
	if err != nil {
		return nil
	}
	filename := "payments-" + time.Now().Format("20060102") + ".csv"

	// Paged by LIMIT/OFFSET over a total order (paid_at, id) rather than
	// exportStream's FindInBatches, whose id keyset only pages correctly when
	// the export is ordered by id.
	page := func(n int) ([]paymentExportRow, error) {
		var rows []paymentExportRow
		err := query.Session(&gorm.Session{}).Offset(n * exportBatchSize).Limit(exportBatchSize).Scan(&rows).Error
		return rows, err
	}
	first, err := page(0)
	if err != nil {
		log.Printf("export %s: %v", filename, err)
		return utils.Internal(c, "Failed to export data")
	}
	return streamCSV(c, filename, paymentsExportHeader, func(w *csv.Writer) error {
		rows := first
		for n := 1; ; n++ {
			for _, r := range rows {
				if err := writeCSVRow(w, paymentExportFields(r)); err != nil {
					return err
				}
			}
			if len(rows) < exportBatchSize {
				return nil
			}
			var err error
			if rows, err = page(n); err != nil {
				return err
			}
		}
	})
}

// paymentExportFields renders one CSV row.
func paymentExportFields(r paymentExportRow) []string {
	money := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	installmentID, dueDate := "", ""
	if r.InstallmentID != nil {
		installmentID = strconv.FormatUint(uint64(*r.InstallmentID), 10)
	}
	if r.InstallmentDueDate != nil {
		dueDate = r.InstallmentDueDate.In(time.Local).Format("2006-01-02")
	}
	createdBy := strings.TrimSpace(utils.DerefString(r.CreatedByFirst) + " " + utils.DerefString(r.CreatedByLast))
	return []string{
		r.PaidAt.In(time.Local).Format("2006-01-02"), utils.DerefString(r.DocumentNumber),
		strconv.FormatUint(uint64(r.DealID), 10), r.DealTitle, utils.DerefString(r.CompanyName),
		money(r.Amount), money(r.WhtAmount), money(utils.RoundSatang(r.Amount + r.WhtAmount)),
		r.Method, installmentID, dueDate, r.Note, createdBy,
	}
}

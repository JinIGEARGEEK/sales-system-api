package handlers

import (
	"encoding/csv"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/igeargeek/sales-system-api/internal/utils"
)

// This file adds a CSV download alongside every /reports/* JSON endpoint in
// reports.go, matching the existing Companies/Contacts/Deals/Products/
// Projects "Export CSV" pattern in export.go. Report result sets are always
// a bounded, already-filtered "problem list" (stalled deals, overdue
// projects, etc.), never a raw table scan — so unlike ExportHandler's
// exportStream (which pages through a potentially six-figure-row table),
// these just write the same in-memory slice reports.go's JSON handlers
// already compute, via the same streamCSV writer, in one pass.

// derefUintStr renders a nullable uint id (e.g. Deal.AssignedTo) as a CSV
// field — empty string when nil, rather than "0" or a literal <nil>.
func derefUintStr(p *uint) string {
	if p == nil {
		return ""
	}
	return strconv.FormatUint(uint64(*p), 10)
}

// LeadSourceConversionExport godoc
// @Summary Export lead source conversion report as CSV (Admin/Sales Manager only)
// @Description CSV download of the lead source conversion report (see GET /reports/lead-source-conversion). FR-CRM-054, FR-CRM-055 (rep filter). Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param date_from query string false "ISO date lower bound (YYYY-MM-DD), filters on created_at"
// @Param date_to query string false "ISO date upper bound (YYYY-MM-DD), filters on created_at"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export lead source conversion"
// @Router /reports/lead-source-conversion/export [get]
func (h *ReportHandler) LeadSourceConversionExport(c *fiber.Ctx) error {
	rows, err := h.fetchLeadSourceConversion(c)
	if err != nil {
		return utils.Internal(c, "Failed to export lead source conversion")
	}
	header := []string{"Source", "Total Leads", "Qualified", "Conversion Rate (%)"}
	return streamCSV(c, "lead-source-conversion.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				string(r.Source), strconv.FormatInt(r.Total, 10), strconv.FormatInt(r.Qualified, 10),
				strconv.FormatFloat(r.ConversionRate, 'f', 1, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ProspectSourceConversionExport godoc
// @Summary Export prospect source conversion report as CSV (Admin/Marketing/Sales Manager/Sales Rep)
// @Description CSV download of the prospect source conversion report (see GET /reports/prospect-source-conversion). Open to Admin, Marketing, Sales Manager, and Sales Rep.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param date_from query string false "ISO date lower bound (YYYY-MM-DD), filters on created_at"
// @Param date_to query string false "ISO date upper bound (YYYY-MM-DD), filters on created_at"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export prospect source conversion"
// @Router /reports/prospect-source-conversion/export [get]
func (h *ReportHandler) ProspectSourceConversionExport(c *fiber.Ctx) error {
	rows, err := h.fetchProspectSourceConversion(c)
	if err != nil {
		return utils.Internal(c, "Failed to export prospect source conversion")
	}
	header := []string{"Source", "Total Prospects", "Converted", "Conversion Rate (%)"}
	return streamCSV(c, "prospect-source-conversion.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.Source, strconv.FormatInt(r.Total, 10), strconv.FormatInt(r.Converted, 10),
				strconv.FormatFloat(r.ConversionRate, 'f', 1, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// CustomersByProductStatusExport godoc
// @Summary Export customers by product status report as CSV (Admin/Sales Manager only)
// @Description CSV download of the customers by product status report (see GET /reports/customers-by-product-status). FR-CRM-056, FR-CRM-055 (company-tag filter). Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param product_id query int false "Filter by Product ID"
// @Param status query string false "Filter by CustomerProduct status"
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export customers by product status"
// @Router /reports/customers-by-product-status/export [get]
func (h *ReportHandler) CustomersByProductStatusExport(c *fiber.Ctx) error {
	rows, err := h.fetchCustomersByProductStatus(c)
	if err != nil {
		return utils.Internal(c, "Failed to export customers by product status")
	}
	header := []string{"Company", "Product ID", "Status", "Start Date"}
	return streamCSV(c, "customers-by-product-status.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.CompanyName, strconv.FormatUint(uint64(r.ProductID), 10), string(r.Status), r.StartDate,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// WinLossReasonsExport godoc
// @Summary Export win/loss reasons report as CSV (Admin/Sales Manager only)
// @Description CSV download of the win/loss reasons report (see GET /reports/win-loss-reasons). FR-CRM-093. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param date_from query string false "ISO date lower bound (YYYY-MM-DD), filters on deals.created_at"
// @Param date_to query string false "ISO date upper bound (YYYY-MM-DD), filters on deals.created_at"
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export win/loss reasons"
// @Router /reports/win-loss-reasons/export [get]
func (h *ReportHandler) WinLossReasonsExport(c *fiber.Ctx) error {
	rows, err := h.fetchWinLossReasons(c)
	if err != nil {
		return utils.Internal(c, "Failed to export win/loss reasons")
	}
	header := []string{"Reason", "Count", "Value"}
	return streamCSV(c, "win-loss-reasons.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.Reason, strconv.FormatInt(r.Count, 10), strconv.FormatFloat(r.Value, 'f', 2, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// StalledDealsExport godoc
// @Summary Export stalled deals report as CSV (Admin/Sales Manager only)
// @Description CSV download of the stalled deals report (see GET /reports/stalled-deals). FR-CRM-094. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param min_days query int false "Minimum days since last activity to be considered stalled (default 14)"
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export stalled deals"
// @Router /reports/stalled-deals/export [get]
func (h *ReportHandler) StalledDealsExport(c *fiber.Ctx) error {
	rows, err := h.fetchStalledDeals(c)
	if err != nil {
		return utils.Internal(c, "Failed to export stalled deals")
	}
	header := []string{"Deal", "Company", "Stage", "Value", "Assigned To", "Last Activity", "Days Stalled"}
	return streamCSV(c, "stalled-deals.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.Title, r.CompanyName, r.Stage, strconv.FormatFloat(r.Value, 'f', 2, 64),
				derefUintStr(r.AssignedTo), r.LastActivityAt.Format("2006-01-02"), strconv.Itoa(r.DaysStalled),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// OutstandingBalanceExport godoc
// @Summary Export outstanding balance report as CSV (Admin/Sales Manager only)
// @Description CSV download of the outstanding balance report (see GET /reports/outstanding-balance). FR-CRM-095. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export outstanding balance"
// @Router /reports/outstanding-balance/export [get]
func (h *ReportHandler) OutstandingBalanceExport(c *fiber.Ctx) error {
	rows, err := h.fetchOutstandingBalance(c)
	if err != nil {
		return utils.Internal(c, "Failed to export outstanding balance")
	}
	header := []string{"Deal", "Company", "Deal Value", "Paid", "Outstanding"}
	return streamCSV(c, "outstanding-balance.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.DealTitle, r.CompanyName, strconv.FormatFloat(r.DealValue, 'f', 2, 64),
				strconv.FormatFloat(r.PaidAmount, 'f', 2, 64), strconv.FormatFloat(r.OutstandingAmount, 'f', 2, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// QuotesExpiringSoonExport godoc
// @Summary Export quotes expiring soon report as CSV (Admin/Sales Manager only)
// @Description CSV download of the quotes expiring soon report (see GET /reports/quotes-expiring-soon). FR-CRM-096. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param within_days query int false "Look-ahead window in days (default 7)"
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export quotes expiring soon"
// @Router /reports/quotes-expiring-soon/export [get]
func (h *ReportHandler) QuotesExpiringSoonExport(c *fiber.Ctx) error {
	rows, err := h.fetchQuotesExpiringSoon(c)
	if err != nil {
		return utils.Internal(c, "Failed to export quotes expiring soon")
	}
	header := []string{"Deal", "Company", "Validity Date", "Total Value"}
	return streamCSV(c, "quotes-expiring-soon.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.DealTitle, r.CompanyName, r.ValidityDate, strconv.FormatFloat(r.TotalValue, 'f', 2, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ContractsStuckExport godoc
// @Summary Export contracts stuck report as CSV (Admin/Sales Manager only)
// @Description CSV download of the contracts stuck report (see GET /reports/contracts-stuck). FR-CRM-097. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param min_days query int false "Minimum days in Draft/Sent status to be considered stuck (default 14)"
// @Param assigned_to query string false "Filter by assigned Sales Rep user ID"
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export contracts stuck"
// @Router /reports/contracts-stuck/export [get]
func (h *ReportHandler) ContractsStuckExport(c *fiber.Ctx) error {
	rows, err := h.fetchContractsStuck(c)
	if err != nil {
		return utils.Internal(c, "Failed to export contracts stuck")
	}
	header := []string{"Deal", "Company", "Status", "Assigned To", "Days Unsigned"}
	return streamCSV(c, "contracts-stuck.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.DealTitle, r.CompanyName, r.Status, derefUintStr(r.AssignedTo), strconv.Itoa(r.DaysInStatus),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ProjectsAtRiskExport godoc
// @Summary Export projects at risk report as CSV (Admin/Sales Manager only)
// @Description CSV download of the projects at risk report (see GET /reports/projects-at-risk). FR-CRM-098. Admin/Sales Manager only.
// @Tags reports
// @Security BearerAuth
// @Produce text/csv
// @Param company_tag query string false "Filter by Company tag"
// @Success 200 {file} file "CSV export"
// @Failure 500 {object} map[string]interface{} "Failed to export projects at risk"
// @Router /reports/projects-at-risk/export [get]
func (h *ReportHandler) ProjectsAtRiskExport(c *fiber.Ctx) error {
	rows, err := h.fetchProjectsAtRisk(c)
	if err != nil {
		return utils.Internal(c, "Failed to export projects at risk")
	}
	header := []string{"Project", "Company", "Status", "Target End Date", "Days Overdue"}
	return streamCSV(c, "projects-at-risk.csv", header, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.Name, r.CompanyName, r.Status, r.TargetEndDate, strconv.Itoa(r.DaysOverdue),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

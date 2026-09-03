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

// LeadSourceConversionExport — GET /reports/lead-source-conversion/export.
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

// ProspectSourceConversionExport — GET /reports/prospect-source-conversion/export.
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

// CustomersByProductStatusExport — GET /reports/customers-by-product-status/export.
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

// WinLossReasonsExport — GET /reports/win-loss-reasons/export.
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

// StalledDealsExport — GET /reports/stalled-deals/export.
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

// OutstandingBalanceExport — GET /reports/outstanding-balance/export.
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

// QuotesExpiringSoonExport — GET /reports/quotes-expiring-soon/export.
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

// ContractsStuckExport — GET /reports/contracts-stuck/export.
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

// ProjectsAtRiskExport — GET /reports/projects-at-risk/export.
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

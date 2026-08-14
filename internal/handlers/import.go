package handlers

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const maxImportSize = 10 * 1024 * 1024

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
		return nil, fmt.Errorf("too large")
	}
	if !strings.HasSuffix(strings.ToLower(fh.Filename), ".csv") {
		return nil, fmt.Errorf("unsupported format")
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
	return rows, nil
}

// ImportCompanies — POST /companies/import. Expects columns: name,industry,size,website.
// Dedupes by name since Company has no email field, per FR-CRM-014.
func (h *ImportHandler) ImportCompanies(c *fiber.Ctx) error {
	rows, err := openImportFile(c)
	if err != nil {
		if err.Error() == "too large" {
			return utils.ErrorResponse(c, fiber.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "File exceeds 10MB limit")
		}
		if err.Error() == "unsupported format" {
			return utils.BadRequest(c, "Only CSV files are supported")
		}
		return utils.BadRequest(c, "Invalid or missing file")
	}

	result := importResult{Errors: []importError{}}
	for i, row := range rows {
		rowNum := i + 2
		if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "name is required"})
			result.Skipped++
			continue
		}
		name := strings.TrimSpace(row[0])
		var industry, size, website string
		if len(row) > 1 {
			industry = strings.TrimSpace(row[1])
		}
		if len(row) > 2 {
			size = strings.TrimSpace(row[2])
		}
		if len(row) > 3 {
			website = strings.TrimSpace(row[3])
		}

		var existing models.Company
		err := h.DB.Where("name = ?", name).First(&existing).Error
		if err == nil {
			existing.Industry, existing.Size, existing.Website = industry, size, website
			if err := h.DB.Save(&existing).Error; err != nil {
				result.Errors = append(result.Errors, importError{Row: rowNum, Message: "failed to update"})
				result.Skipped++
				continue
			}
			result.Updated++
			continue
		}

		company := models.Company{Name: name, Industry: industry, Size: size, Website: website, Status: models.StatusActive}
		if err := h.DB.Create(&company).Error; err != nil {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "failed to create"})
			result.Skipped++
			continue
		}
		result.Created++
	}
	return utils.OK(c, result)
}

// ImportContacts — POST /contacts/import. Expects columns:
// company_id,name,email,phone,role_title. Dedupes by email per FR-CRM-014.
func (h *ImportHandler) ImportContacts(c *fiber.Ctx) error {
	rows, err := openImportFile(c)
	if err != nil {
		if err.Error() == "too large" {
			return utils.ErrorResponse(c, fiber.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "File exceeds 10MB limit")
		}
		if err.Error() == "unsupported format" {
			return utils.BadRequest(c, "Only CSV files are supported")
		}
		return utils.BadRequest(c, "Invalid or missing file")
	}

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
		name := strings.TrimSpace(row[1])
		var email, phone, roleTitle string
		if len(row) > 2 {
			email = strings.TrimSpace(row[2])
		}
		if len(row) > 3 {
			phone = strings.TrimSpace(row[3])
		}
		if len(row) > 4 {
			roleTitle = strings.TrimSpace(row[4])
		}

		if email != "" {
			var existing models.Contact
			err := h.DB.Where("email = ?", email).First(&existing).Error
			if err == nil {
				existing.Name, existing.Phone, existing.RoleTitle, existing.CompanyID = name, phone, roleTitle, companyID
				if err := h.DB.Save(&existing).Error; err != nil {
					result.Errors = append(result.Errors, importError{Row: rowNum, Message: "failed to update"})
					result.Skipped++
					continue
				}
				result.Updated++
				continue
			}
		}

		contact := models.Contact{
			CompanyID: companyID, Name: name, Email: email, Phone: phone, RoleTitle: roleTitle,
			Status: models.StatusActive,
		}
		if err := h.DB.Create(&contact).Error; err != nil {
			result.Errors = append(result.Errors, importError{Row: rowNum, Message: "failed to create"})
			result.Skipped++
			continue
		}
		result.Created++
	}
	return utils.OK(c, result)
}

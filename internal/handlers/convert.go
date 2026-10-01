package handlers

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// errAlreadyConverted is returned from inside a Convert transaction when the
// locked source row turns out to be converted already — a concurrent request
// won the race after this one's pre-check. Both Converts map it to 409.
var errAlreadyConverted = errors.New("already converted")

// convertInputError is a caller mistake in a Convert request's explicit
// company_id/contact_id, found inside the transaction: a missing record (404)
// or a contact under a different company (422).
type convertInputError struct {
	notFound bool
	field    string
	msg      string
}

func (e *convertInputError) Error() string { return e.msg }

// writeConvertTxError writes the response for an error returned by a Convert
// transaction and reports whether it was one of the known ones above; the
// caller writes its own 500 otherwise.
func writeConvertTxError(c *fiber.Ctx, err error, conflictMsg string) bool {
	var inputErr *convertInputError
	switch {
	case errors.Is(err, errAlreadyConverted):
		_ = utils.Conflict(c, conflictMsg)
	case errors.As(err, &inputErr) && inputErr.notFound:
		_ = utils.NotFound(c, inputErr.msg)
	case errors.As(err, &inputErr):
		_ = utils.ValidationError(c, inputErr.msg, map[string][]string{inputErr.field: {"invalid"}})
	default:
		return false
	}
	return true
}

// lockForConvert re-reads the source row (a *models.Lead or
// *models.Prospect, already loaded once) with SELECT ... FOR UPDATE, so two
// concurrent Converts of the same record serialize here: the second blocks
// until the first commits, then re-reads the row already stamped. The
// caller re-checks its converted_* column on the fresh value.
func lockForConvert(tx *gorm.DB, row interface{}, id uint) error {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(row, id).Error
}

// resolveOrCreateCompany is the Company half of Lead and Prospect Convert:
//
//   - explicitID (the request's company_id) always wins; one that doesn't
//     exist is a 404 rather than a silent fallback, since the caller chose it.
//   - Otherwise fallbackID (the source record's own company_id) is reused.
//     If that Company has since been soft-deleted, a fresh one is created
//     instead of failing over a Company the caller didn't pick.
//   - With neither, a new Company is created.
//
// A created Company takes names.explicit (the request's company_name), else
// the soft-deleted fallback Company's name, else names.contact (the source
// record's name). With none of those it's a 422 asking for company_id or
// company_name.
func resolveOrCreateCompany(tx *gorm.DB, explicitID, fallbackID *uint, names newCompanyNames) (models.Company, error) {
	var company models.Company
	switch {
	case explicitID != nil:
		if err := tx.First(&company, *explicitID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return models.Company{}, &convertInputError{notFound: true, field: "company_id", msg: "Company not found"}
			}
			return models.Company{}, err
		}
		return company, nil
	case fallbackID != nil:
		err := tx.First(&company, *fallbackID).Error
		if err == nil {
			return company, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Company{}, err
		}
		var gone models.Company
		if err := tx.Unscoped().Select("name").First(&gone, *fallbackID).Error; err == nil {
			names.former = gone.Name
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Company{}, err
		}
	}

	name := names.pick()
	if name == "" {
		return models.Company{}, &convertInputError{field: "company_id", msg: "company_id or company_name is required to create the company"}
	}
	company = models.Company{Name: name, Status: models.StatusActive}
	if err := tx.Create(&company).Error; err != nil {
		return models.Company{}, err
	}
	return company, nil
}

// newCompanyNames are the candidate names for a Company Convert has to
// create, in resolveOrCreateCompany's order of preference.
type newCompanyNames struct {
	explicit string // the request's company_name
	former   string // the source record's soft-deleted Company, if any
	contact  string // the source Lead/Prospect's own name
}

func (n newCompanyNames) pick() string {
	for _, v := range []string{n.explicit, n.former, n.contact} {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// resolveOrCreateContact is the Contact half of Lead and Prospect Convert:
// explicitID (the request's contact_id) if given, which must exist and
// belong to companyID; else the oldest Contact in companyID with the same
// email (case-insensitive), so a person the Company already has isn't
// duplicated; else a new Contact under companyID from the source record's
// name/email/phone.
func resolveOrCreateContact(tx *gorm.DB, explicitID *uint, companyID uint, name, email, phone string) (models.Contact, error) {
	var contact models.Contact
	if explicitID != nil {
		if err := tx.First(&contact, *explicitID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return models.Contact{}, &convertInputError{notFound: true, field: "contact_id", msg: "Contact not found"}
			}
			return models.Contact{}, err
		}
		if contact.CompanyID != companyID {
			return models.Contact{}, &convertInputError{field: "contact_id", msg: "contact_id does not belong to the company"}
		}
		return contact, nil
	}
	if e := utils.NormalizeEmail(email); e != "" {
		err := tx.Where("company_id = ? AND LOWER(TRIM(email)) = ?", companyID, e).Order("id").First(&contact).Error
		if err == nil {
			return contact, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Contact{}, err
		}
	}
	contact = models.Contact{
		CompanyID: companyID, Name: name, Email: email, Phone: phone,
		Status: models.StatusActive,
	}
	if err := tx.Create(&contact).Error; err != nil {
		return models.Contact{}, err
	}
	return contact, nil
}

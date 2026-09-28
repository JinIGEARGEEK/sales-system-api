package handlers

import (
	"errors"

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
// or a contact under a different company (422). Previously both surfaced as
// a generic 500.
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

// resolveOrCreateCompany implements the Company-resolution half of both
// LeadHandler.Convert and ProspectHandler.Convert — previously duplicated
// almost line-for-line between the two:
//
//   - explicitID (the caller's own req.CompanyID override, when given) always
//     wins, even if it differs from whatever Company the source record was
//     already linked to — a caller mistake here is worth failing loudly on
//     (a not-found explicitID returns an error rather than falling back).
//   - Otherwise, fallbackID (the source Lead/Prospect's own CompanyID, if
//     it has one) is reused as-is. Unlike explicitID, this id was never
//     caller-supplied on this particular request — if the Company it points
//     to has since been soft-deleted, fall back to creating a fresh one
//     rather than failing the whole conversion over a Company the caller
//     never chose here in the first place.
//   - With neither, a brand-new empty Company is created.
func resolveOrCreateCompany(tx *gorm.DB, explicitID, fallbackID *uint) (models.Company, error) {
	var company models.Company
	switch {
	case explicitID != nil:
		if err := tx.First(&company, *explicitID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return models.Company{}, &convertInputError{notFound: true, field: "company_id", msg: "Company not found"}
			}
			return models.Company{}, err
		}
	case fallbackID != nil:
		if err := tx.First(&company, *fallbackID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return models.Company{}, err
			}
			company = models.Company{Status: models.StatusActive}
			if err := tx.Create(&company).Error; err != nil {
				return models.Company{}, err
			}
		}
	default:
		company = models.Company{Status: models.StatusActive}
		if err := tx.Create(&company).Error; err != nil {
			return models.Company{}, err
		}
	}
	return company, nil
}

// resolveOrCreateContact implements the Contact-resolution half of both
// LeadHandler.Convert and ProspectHandler.Convert — reuse explicitID
// (req.ContactID) if given (it must exist and belong to companyID),
// otherwise create a new Contact under companyID seeded from the source
// Lead/Prospect's own name/email/phone.
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
	contact = models.Contact{
		CompanyID: companyID, Name: name, Email: email, Phone: phone,
		Status: models.StatusActive,
	}
	if err := tx.Create(&contact).Error; err != nil {
		return models.Contact{}, err
	}
	return contact, nil
}

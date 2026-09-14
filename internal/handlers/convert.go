package handlers

import (
	"errors"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
)

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
// (req.ContactID) verbatim if given, otherwise create a new Contact under
// companyID seeded from the source Lead/Prospect's own name/email/phone.
func resolveOrCreateContact(tx *gorm.DB, explicitID *uint, companyID uint, name, email, phone string) (models.Contact, error) {
	var contact models.Contact
	if explicitID != nil {
		if err := tx.First(&contact, *explicitID).Error; err != nil {
			return models.Contact{}, err
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

package utils

import (
	"net/mail"
	"strings"
)

// AllowedEmailDomain restricts staff login emails to the company domain.
const AllowedEmailDomain = "igeargeek.com"

// IsValidCompanyEmail reports whether email is a syntactically valid address
// on AllowedEmailDomain.
func IsValidCompanyEmail(email string) bool {
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	return strings.EqualFold(email[at+1:], AllowedEmailDomain)
}

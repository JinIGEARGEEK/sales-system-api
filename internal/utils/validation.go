package utils

import (
	"net/mail"
	"strings"

	"github.com/gofiber/fiber/v2"
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

// Field pairs a request field's JSON name with its submitted value, for RequireFields.
type Field struct {
	Name  string
	Value string
}

// RequireFields returns a 422 ValidationError naming every field whose Value is
// empty (fields map keyed by Name, each `["required"]`, per api-system-spec.md
// §1.5), or nil if none are empty. Checks all fields at once rather than
// failing on the first empty one, so the caller sees every missing field in a
// single response.
func RequireFields(c *fiber.Ctx, fields ...Field) error {
	var missing []string
	errFields := make(map[string][]string)
	for _, f := range fields {
		if f.Value == "" {
			missing = append(missing, f.Name)
			errFields[f.Name] = []string{"required"}
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var verb string
	if len(missing) == 1 {
		verb = "is required"
	} else {
		verb = "are required"
	}
	return ValidationError(c, strings.Join(missing, ", ")+" "+verb, errFields)
}

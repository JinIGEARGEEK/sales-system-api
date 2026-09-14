package utils

import "regexp"

var (
	websiteHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	phonePattern       = regexp.MustCompile(`^[0-9+()\-\s]{6,20}$`)
)

// IsValidWebsiteDomain checks an already-extracted domain (utils.ExtractDomain)
// against websiteHostPattern. Takes the domain rather than the raw
// Website/URL string so CompanyHandler — which needs the extracted domain
// anyway, to store in Company.Domain — can extract it once and validate that
// same value, instead of paying for ExtractDomain's parsing twice per
// request. Deliberately not a strict RFC 1034 validator — the goal is
// catching "not a website" typos (blank text, half-pasted strings), not
// bouncing valid-but-unusual real-world domains. Doesn't itself treat an
// empty domain as valid (an empty string never matches websiteHostPattern);
// CompanyHandler only calls this when form.Website != "" in the first place,
// so an optional/omitted website never reaches here at all.
func IsValidWebsiteDomain(domain string) bool {
	return websiteHostPattern.MatchString(domain)
}

// IsValidEmail is a lenient format check for Contact.Email — empty is valid
// (optional field). Shares isValidEmailAddress (validation.go) with
// IsValidCompanyEmail rather than a separate hand-rolled regex — net/mail's
// parser handles real RFC 5322 edge cases (quoted local parts, comments,
// etc.) a regex would get wrong in one direction or the other. Unlike
// IsValidCompanyEmail, there's no domain restriction — any syntactically
// valid address is accepted.
func IsValidEmail(v string) bool {
	if v == "" {
		return true
	}
	return isValidEmailAddress(v)
}

// IsValidPhone is a lenient format check for Contact.Phone — empty is valid.
// Accepts digits, spaces, +, -, ( and ) — covers "+66-2-000-0000", "(02) 000
// 0000", "0200000000" — and just bounds length so obvious non-phone garbage
// ("n/a", a full sentence) doesn't get silently stored as a phone number.
func IsValidPhone(v string) bool {
	if v == "" {
		return true
	}
	return phonePattern.MatchString(v)
}

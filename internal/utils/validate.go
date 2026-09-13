package utils

import "regexp"

var (
	websiteHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	phonePattern       = regexp.MustCompile(`^[0-9+()\-\s]{6,20}$`)
)

// IsValidWebsiteDomain checks an already-extracted domain (utils.ExtractDomain)
// against websiteHostPattern — split out from IsValidWebsite so a caller that
// needs the domain anyway (CompanyHandler, which stores it in Company.Domain)
// can extract it once and validate that same value, instead of paying for
// ExtractDomain's parsing twice per request.
func IsValidWebsiteDomain(domain string) bool {
	return websiteHostPattern.MatchString(domain)
}

// IsValidWebsite is a lenient format check for Company.Website — empty is
// valid (the field is optional). It reuses ExtractDomain's own scheme/path
// stripping (domain.go) so "acme.com", "www.acme.com", and
// "https://acme.com/about" are all judged the same way, and only rejects the
// result if it doesn't look like a real host at all (no dot, spaces, or
// other garbage). Deliberately not a strict RFC 1034 validator — the goal is
// catching "not a website" typos (blank text, half-pasted strings), not
// bouncing valid-but-unusual real-world domains.
func IsValidWebsite(v string) bool {
	if v == "" {
		return true
	}
	return IsValidWebsiteDomain(ExtractDomain(v))
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

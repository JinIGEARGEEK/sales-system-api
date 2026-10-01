package utils

import "strings"

// NormalizeEmail is the form two emails are compared in for duplicate
// detection and import matching: trimmed and lowercased. Stored values keep
// whatever casing the user typed.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizePhone is the form two phone numbers are compared in: digits only,
// with Thailand's +66 country code written as the leading 0 of the national
// number, so "+66 81-234-5678", "081 234 5678" and "0812345678" are all
// "0812345678". Only a "66" followed by at least 8 more digits counts as the
// country code, so a short local number that happens to start with 66 is
// left alone. Returns "" when v has no digits.
//
// NormalizedPhoneSQL is the same rule in SQL; keep the two in step.
func NormalizePhone(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if strings.HasPrefix(d, "66") && len(d) >= 10 {
		d = "0" + d[2:]
	}
	return d
}

// NormalizedPhoneSQL returns a Postgres expression computing NormalizePhone
// over column, for matching stored numbers against a normalized one.
func NormalizedPhoneSQL(column string) string {
	return `regexp_replace(regexp_replace(` + column + `, '\D', '', 'g'), '^66(\d{8,})$', '0\1')`
}

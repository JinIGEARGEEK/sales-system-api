package utils

import (
	"strings"
	"unicode"
)

// NormalizeTaxID drops the spaces and dashes people type inside a tax ID
// ("0-1055-55555-55-5", "0105 555 555 555"), so one tax ID is always stored,
// and matched by ?tax_id=, the same way. Any Unicode space or dash counts,
// since pasted values often carry a non-breaking space or an en dash. Other
// characters are kept: this is not a format check.
func NormalizeTaxID(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Pd, r) {
			return -1
		}
		return r
	}, v)
}

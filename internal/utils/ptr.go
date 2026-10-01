package utils

import "strconv"

// Nullable-column helpers: models use *string/*uint for optional fields.

// DerefString returns *s, or "" for nil.
func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// StringOrDefault returns *s if non-nil and non-empty, else def.
func StringOrDefault(s *string, def string) string {
	if s != nil && *s != "" {
		return *s
	}
	return def
}

// UintPtrString renders a nullable id (e.g. Deal.AssignedTo) as text: ""
// when nil, rather than "0" or a literal <nil> (CSV exports).
func UintPtrString(p *uint) string {
	if p == nil {
		return ""
	}
	return strconv.FormatUint(uint64(*p), 10)
}

// UintPtrEqual reports whether two nullable ids hold the same value (both
// nil counts as equal).
func UintPtrEqual(a, b *uint) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

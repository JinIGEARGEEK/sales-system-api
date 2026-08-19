package utils

import "strings"

// ExtractDomain normalizes a website value down to a bare, lowercase domain
// for comparison purposes: strips scheme (http/https), a leading "www.", any
// path/query/fragment, and a trailing slash. Returns "" if website is blank
// or has no discernible host. Used both to populate Company.Domain at write
// time and to backfill it for pre-existing rows (internal/database).
func ExtractDomain(website string) string {
	s := strings.TrimSpace(strings.ToLower(website))
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, "://"); idx != -1 {
		s = s[idx+3:]
	}
	// Drop path/query/fragment.
	if idx := strings.IndexAny(s, "/?#"); idx != -1 {
		s = s[:idx]
	}
	// Drop userinfo if present (rare in a website column, but be safe).
	if idx := strings.LastIndex(s, "@"); idx != -1 {
		s = s[idx+1:]
	}
	// Drop port.
	if idx := strings.LastIndex(s, ":"); idx != -1 {
		s = s[:idx]
	}
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimSuffix(s, ".")
	return s
}

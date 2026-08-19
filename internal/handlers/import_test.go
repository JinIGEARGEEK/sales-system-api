package handlers

import (
	"testing"

	"github.com/igeargeek/sales-system-api/internal/utils"
)

func TestExtractDomain(t *testing.T) {
	cases := []struct {
		name    string
		website string
		want    string
	}{
		{"empty", "", ""},
		{"bare domain", "acme.com", "acme.com"},
		{"https scheme", "https://acme.com", "acme.com"},
		{"http scheme with www", "http://www.acme.com", "acme.com"},
		{"trailing slash and path", "https://www.acme.com/about-us/", "acme.com"},
		{"uppercase", "HTTPS://WWW.ACME.COM", "acme.com"},
		{"with port", "https://acme.com:8080/path", "acme.com"},
		{"query string", "http://acme.com?utm_source=x", "acme.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := utils.ExtractDomain(tc.website); got != tc.want {
				t.Errorf("utils.ExtractDomain(%q) = %q, want %q", tc.website, got, tc.want)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	if got := normalizeName("  Acme Co.  "); got != "acme co." {
		t.Errorf("normalizeName trimmed/lowered mismatch: got %q", got)
	}
}

// fakeCompanyMatcher mirrors the matching logic used by findExistingCompany's
// domain-comparison loop, without requiring a live DB, to pin down the
// intended dedup semantics for Company import rows.
func matchesExisting(rowName, rowWebsite, existingName, existingWebsite string) bool {
	rowDomain := utils.ExtractDomain(rowWebsite)
	existingDomain := utils.ExtractDomain(existingWebsite)
	if rowDomain != "" && existingDomain != "" {
		return rowDomain == existingDomain
	}
	return normalizeName(rowName) == normalizeName(existingName)
}

func TestCompanyDedupMatching(t *testing.T) {
	t.Run("same domain matches despite different name casing", func(t *testing.T) {
		if !matchesExisting("ACME CO.", "https://www.acme.com/contact", "Acme Co", "http://acme.com") {
			t.Error("expected domain match despite differing name casing/spelling")
		}
	})

	t.Run("no website falls back to normalized name match", func(t *testing.T) {
		if !matchesExisting("  Acme Co  ", "", "acme co", "") {
			t.Error("expected case/whitespace-insensitive name match when no website present")
		}
	})

	t.Run("different companies do not false-positive match", func(t *testing.T) {
		if matchesExisting("Acme Co", "https://acme.com", "Beta Inc", "https://beta.com") {
			t.Error("different domains and names must not match")
		}
		if matchesExisting("Acme Co", "", "Beta Inc", "") {
			t.Error("different names with no website must not match")
		}
	})
}

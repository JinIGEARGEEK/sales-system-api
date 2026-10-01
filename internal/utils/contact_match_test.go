package utils

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"n/a":             "",
		"081-234-5678":    "0812345678",
		"(02) 000 0000":   "020000000",
		"+66 81 234 5678": "0812345678",
		"+66-2-000-0000":  "020000000",
		"66812345678":     "0812345678",
		"6612":            "6612", // too short to be the country code
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Jane.Doe@Example.COM "); got != "jane.doe@example.com" {
		t.Errorf("NormalizeEmail = %q", got)
	}
}

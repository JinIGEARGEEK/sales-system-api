package utils

import "testing"

func TestNormalizeTaxID(t *testing.T) {
	cases := map[string]string{
		"0105555555555":      "0105555555555",
		"0-1055-55555-55-5":  "0105555555555",
		" 0105 555 555 555 ": "0105555555555",
		"0105 555–555555":    "0105555555555",
		"TH0105555555555":    "TH0105555555555",
		"--":                 "",
		"":                   "",
	}
	for in, want := range cases {
		if got := NormalizeTaxID(in); got != want {
			t.Errorf("NormalizeTaxID(%q) = %q, want %q", in, got, want)
		}
	}
}

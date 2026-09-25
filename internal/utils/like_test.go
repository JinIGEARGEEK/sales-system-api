package utils

import "testing"

func TestLikePattern(t *testing.T) {
	cases := map[string]string{
		"acme":    "%acme%",
		"50%":     `%50\%%`,
		"a_b":     `%a\_b%`,
		`c:\temp`: `%c:\\temp%`,
		`\%_`:     `%\\\%\_%`,
		"":        "%%",
	}
	for in, want := range cases {
		if got := LikePattern(in); got != want {
			t.Errorf("LikePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

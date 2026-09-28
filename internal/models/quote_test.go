package models

import "testing"

// TestParseFlexDate guards the dual-format fallback: RFC 3339, then a bare
// "2006-01-02".
func TestParseFlexDate(t *testing.T) {
	cases := []struct {
		name   string
		input  *string
		wantOK bool
	}{
		{"nil", nil, false},
		{"empty string", strPtr(""), false},
		{"RFC3339", strPtr("2026-09-01T00:00:00.000Z"), true},
		{"bare date", strPtr("2026-09-01"), true},
		{"garbage", strPtr("not-a-date"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := ParseFlexDate(tc.input)
			if ok != tc.wantOK {
				t.Errorf("ParseFlexDate(%v) ok = %v, want %v", tc.input, ok, tc.wantOK)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

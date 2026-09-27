package utils

import (
	"reflect"
	"testing"

	"github.com/igeargeek/sales-system-api/internal/models"
)

func TestCompanyPartyLines(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name    string
		company models.Company
		want    []string
	}{
		{"nothing set", models.Company{}, nil},
		{"head office", models.Company{Address: str("1 Silom Rd, Bangkok"), PostalCode: str("10500"), TaxID: str("0105555555555"), BranchCode: str("00000")},
			[]string{"Address: 1 Silom Rd, Bangkok 10500", "Tax ID: 0105555555555 (Head office)"}},
		{"numbered branch", models.Company{TaxID: str("0105555555555"), BranchCode: str("00001")},
			[]string{"Tax ID: 0105555555555 (Branch 00001)"}},
		{"tax ID without branch", models.Company{TaxID: str("0105555555555")},
			[]string{"Tax ID: 0105555555555"}},
		{"branch without tax ID", models.Company{BranchCode: str("00002")},
			[]string{"Branch: 00002"}},
		{"head office without tax ID", models.Company{BranchCode: str("00000")},
			[]string{"Branch: Head office"}},
		{"postal code only", models.Company{PostalCode: str("10110")},
			[]string{"Address: 10110"}},
	}
	for _, tc := range cases {
		if got := CompanyPartyLines(tc.company); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: CompanyPartyLines() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

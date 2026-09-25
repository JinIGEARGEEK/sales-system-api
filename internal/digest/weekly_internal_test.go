package digest

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/overview"
)

// baht matches the page's priceFormatCompact ('0,0.[0]a'): one decimal at
// most, and a value that rounds up to the next unit moves to it.
func TestBaht(t *testing.T) {
	for in, want := range map[float64]string{
		0: "฿0", 950: "฿1K", 1550: "฿1.6K", 850000: "฿850K",
		999949: "฿999.9K", 999950: "฿1M", 4700000: "฿4.7M", 12000000: "฿12M",
	} {
		assert.Equal(t, want, baht(in), "%v", in)
	}
}

// cohortLine rounds the percentage the way the page does (2 of 7 = 29%).
func TestCohortLineRounds(t *testing.T) {
	assert.Contains(t, cohortLine(overview.Cohort{Cohort: 7, Converted: 2}, "Prospects", "became Leads"), "(29%)")
	assert.Empty(t, cohortLine(overview.Cohort{}, "Prospects", "became Leads"))
}

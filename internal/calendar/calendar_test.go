package calendar

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A timestamp is read by its server-local date, so a JS Date for local
// midnight on the 1st ("…T17:00:00.000Z" the day before) stays on the 1st.
func TestParseLocalDay(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2026-10-01", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-09-30T17:00:00.000Z", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-10-01T00:00:00.000Z", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-10-01T23:59:00+07:00", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), true},
		{"not-a-date", time.Time{}, false},
		{"", time.Time{}, false},
	}
	for _, tc := range cases {
		got, ok := ParseLocalDay(tc.in)
		assert.Equal(t, tc.ok, ok, tc.in)
		assert.True(t, got.Equal(tc.want), "%s: got %s", tc.in, got)
	}
}

func TestLocalDaysBetween(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.Local)
	assert.Equal(t, 0, LocalDaysBetween(time.Date(2026, 9, 27, 0, 0, 0, 0, time.Local), now))
	assert.Equal(t, 31, LocalDaysBetween(time.Date(2026, 8, 27, 23, 0, 0, 0, time.Local), now))
	assert.Equal(t, -3, LocalDaysBetween(time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local), now))
	assert.Equal(t, 1, LocalDaysBetween(time.Date(2026, 9, 27, 16, 59, 0, 0, time.UTC), time.Date(2026, 9, 27, 17, 0, 0, 0, time.UTC)), "local midnight")
}

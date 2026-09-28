package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useICT sets time.Local to Bangkok's fixed +07:00 (the Dockerfile's TZ)
// for the test, so it doesn't depend on the machine's zone.
func useICT(t *testing.T) *time.Location {
	t.Helper()
	saved := time.Local
	ict := time.FixedZone("ICT", 7*3600)
	time.Local = ict
	t.Cleanup(func() { time.Local = saved })
	return ict
}

// ParseDateRange reads both bounds as server-local days, date_to inclusive:
// [from's local midnight, the local midnight after to).
func TestParseDateRange(t *testing.T) {
	ict := useICT(t)

	r, _, fields := ParseDateRange("date_from", "2026-09-10", "date_to", "2026-09-12")
	require.Nil(t, fields)
	require.NotNil(t, r.From)
	require.NotNil(t, r.ToExclusive)
	assert.True(t, r.From.Equal(time.Date(2026, 9, 10, 0, 0, 0, 0, ict)), "got %s", r.From)
	assert.True(t, r.From.Equal(time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)), "local midnight, not UTC")
	assert.True(t, r.ToExclusive.Equal(time.Date(2026, 9, 13, 0, 0, 0, 0, ict)), "got %s", r.ToExclusive)

	r, _, fields = ParseDateRange("date_from", "", "date_to", "")
	require.Nil(t, fields)
	assert.True(t, r.IsZero())

	r, _, fields = ParseDateRange("date_from", "2026-09-10", "date_to", "2026-09-10")
	require.Nil(t, fields, "a one-day range is valid")
	assert.Equal(t, 24*time.Hour, r.ToExclusive.Sub(*r.From))

	_, msg, fields := ParseDateRange("date_from", "10/09/2026", "date_to", "")
	assert.Equal(t, "date_from is invalid", msg)
	assert.Contains(t, fields, "date_from")

	_, _, fields = ParseDateRange("from", "", "to", "2026-02-30")
	assert.Contains(t, fields, "to", "errors name the param actually sent")

	_, msg, fields = ParseDateRange("date_from", "2026-09-10", "date_to", "2026-09-09")
	assert.Equal(t, "date_to is before date_from", msg)
	assert.Equal(t, []string{"must not be before date_from"}, fields["date_to"])
}

// A timestamp is read by its server-local date, so a JS Date for local
// midnight on the 1st ("…T17:00:00.000Z" the day before) stays on the 1st.
func TestParseLocalCalendarDay(t *testing.T) {
	useICT(t)
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
		got, ok := ParseLocalCalendarDay(tc.in)
		assert.Equal(t, tc.ok, ok, tc.in)
		assert.True(t, got.Equal(tc.want), "%s: got %s", tc.in, got)
	}
}

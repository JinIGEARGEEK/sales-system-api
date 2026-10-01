package utils

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ParseDateRange reads both bounds as server-local days, date_to inclusive:
// [from's local midnight, the local midnight after to).
func TestParseDateRange(t *testing.T) {
	r, err := ParseDateRange("date_from", "2026-09-10", "date_to", "2026-09-12")
	require.NoError(t, err)
	require.NotNil(t, r.From)
	require.NotNil(t, r.ToExclusive)
	assert.True(t, r.From.Equal(time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)), "got %s", r.From)
	assert.True(t, r.From.Equal(time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)), "local midnight, not UTC")
	assert.True(t, r.ToExclusive.Equal(time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local)), "got %s", r.ToExclusive)

	r, err = ParseDateRange("date_from", "", "date_to", "")
	require.NoError(t, err)
	assert.True(t, r.IsZero())

	r, err = ParseDateRange("date_from", "2026-09-10", "date_to", "2026-09-10")
	require.NoError(t, err, "a one-day range is valid")
	assert.Equal(t, 24*time.Hour, r.ToExclusive.Sub(*r.From))

	rangeErr := func(err error) *DateRangeError {
		t.Helper()
		var dre *DateRangeError
		require.True(t, errors.As(err, &dre), "got %v", err)
		return dre
	}

	_, err = ParseDateRange("date_from", "10/09/2026", "date_to", "")
	dre := rangeErr(err)
	assert.Equal(t, "date_from is invalid", dre.Message)
	assert.Contains(t, dre.Fields, "date_from")

	_, err = ParseDateRange("from", "", "to", "2026-02-30")
	assert.Contains(t, rangeErr(err).Fields, "to", "errors name the param actually sent")

	_, err = ParseDateRange("date_from", "2026-09-10", "date_to", "2026-09-09")
	dre = rangeErr(err)
	assert.Equal(t, "date_to is before date_from", dre.Message)
	assert.Equal(t, []string{"must not be before date_from"}, dre.Fields["date_to"])
}

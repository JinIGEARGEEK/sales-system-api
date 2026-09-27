package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bare date is server-local midnight (Bangkok), not UTC midnight, which
// would drop everything updated between 00:00 and 07:00 that day.
func TestParseTimeBound_BareDateIsLocalMidnight(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("ICT", 7*3600)
	t.Cleanup(func() { time.Local = saved })

	got, err := parseTimeBound("2026-09-27")
	require.NoError(t, err)
	assert.True(t, got.Equal(time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)), "got %s", got)

	got, err = parseTimeBound("2026-09-27T00:00:00+07:00")
	require.NoError(t, err)
	assert.True(t, got.Equal(time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)))

	_, err = parseTimeBound("27/09/2026")
	assert.Error(t, err)
}

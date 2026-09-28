package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestMonthBounds_NoRolloverAtMonthEnd guards the trend labels on the
// 29th-31st: stepping now.AddDate(0, ±i, 0) from 31 March would normalize
// "31 February" to 3 March. monthBounds steps from the 1st, in server-local
// time.
func TestMonthBounds_NoRolloverAtMonthEnd(t *testing.T) {
	ict := time.Local

	// 31 March 01:00 Bangkok is still 30 March in UTC.
	now := time.Date(2026, 3, 30, 18, 0, 0, 0, time.UTC)
	start := thisMonthStart(now)
	assert.True(t, start.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, ict)), "got %s", start)

	bounds := monthBounds(start.AddDate(0, -5, 0), 6)
	labels := make([]string, 0, len(bounds))
	for _, b := range bounds {
		labels = append(labels, b.Format("Jan"))
		assert.Equal(t, 1, b.Day())
		assert.Equal(t, ict, b.Location())
	}
	assert.Equal(t, []string{"Oct", "Nov", "Dec", "Jan", "Feb", "Mar", "Apr"}, labels)
}

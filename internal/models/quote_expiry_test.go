package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestEffectiveStatusAt_ExpiresAfterLastValidLocalDay guards validity_date
// as the quote's last valid day in server-local time (Bangkok): a bare date
// used to parse as UTC midnight, so the quote showed Expired from 07:00
// Bangkok on its last valid day.
func TestEffectiveStatusAt_ExpiresAfterLastValidLocalDay(t *testing.T) {
	saved := time.Local
	ict := time.FixedZone("ICT", 7*3600)
	time.Local = ict
	t.Cleanup(func() { time.Local = saved })

	cases := []struct {
		name     string
		validity string
		now      time.Time
		want     QuoteStatus
	}{
		{"last valid day, morning", "2026-09-28", time.Date(2026, 9, 28, 9, 0, 0, 0, ict), QuoteStatusSent},
		{"last valid day, just before midnight", "2026-09-28", time.Date(2026, 9, 28, 23, 59, 0, 0, ict), QuoteStatusSent},
		{"day after", "2026-09-28", time.Date(2026, 9, 29, 0, 1, 0, 0, ict), QuoteStatusExpired},
		// A JS Date for local midnight on the 28th serializes to the 27th UTC.
		{"JS Date, last valid day", "2026-09-27T17:00:00.000Z", time.Date(2026, 9, 28, 20, 0, 0, 0, ict), QuoteStatusSent},
		{"JS Date, day after", "2026-09-27T17:00:00.000Z", time.Date(2026, 9, 29, 0, 1, 0, 0, ict), QuoteStatusExpired},
		// now given in another zone is still read as the local day.
		{"now in UTC", "2026-09-28", time.Date(2026, 9, 28, 16, 0, 0, 0, time.UTC), QuoteStatusSent},
		{"now in UTC, local next day", "2026-09-28", time.Date(2026, 9, 28, 17, 30, 0, 0, time.UTC), QuoteStatusExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote{Status: QuoteStatusSent, ValidityDate: strPtr(tc.validity)}
			assert.Equal(t, tc.want, q.EffectiveStatusAt(tc.now))
		})
	}

	draft := Quote{Status: QuoteStatusDraft, ValidityDate: strPtr("2020-01-01")}
	assert.Equal(t, QuoteStatusDraft, draft.EffectiveStatusAt(time.Now()), "only a Sent quote expires")
	noDate := Quote{Status: QuoteStatusSent}
	assert.Equal(t, QuoteStatusSent, noDate.EffectiveStatusAt(time.Now()))
}

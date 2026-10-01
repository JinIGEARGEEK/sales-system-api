// Package calendar holds the calendar-day helpers: the one place day
// differences are computed.
//
// A "day" here is a Y-M-D pinned to UTC midnight — the shape Postgres hands
// a `type:date` column back in — and days are compared by calendar date,
// never by elapsed hours, so the server's TZ can't shift them. "Local" means
// the server's zone (time.Local; TZ=Asia/Bangkok in production).
package calendar

import (
	"errors"
	"time"
)

const dateLayout = "2006-01-02"

// ErrInvalidDate is returned by Parse.
var ErrInvalidDate = errors.New("must be a YYYY-MM-DD date or an RFC 3339 timestamp")

// Day drops t's time of day, keeping the Y-M-D as seen in t's own location.
func Day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// LocalDay is Day of t read in server-local time.
func LocalDay(t time.Time) time.Time {
	return Day(t.In(time.Local))
}

// Today is the server-local calendar date of now, for comparing against
// date columns.
func Today(now time.Time) time.Time {
	return LocalDay(now)
}

// QuarterStart is the first day of day's calendar quarter (1 January,
// April, July or October), as a Day. The next quarter starts 3 months on.
func QuarterStart(day time.Time) time.Time {
	return time.Date(day.Year(), (day.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
}

// DaysUntil counts whole calendar days from `from` to `to`, each read as the
// Y-M-D in its own location — for date values (date columns, parsed
// YYYY-MM-DD strings). Negative when `to` is earlier.
func DaysUntil(from, to time.Time) int {
	return int(Day(to).Sub(Day(from)).Hours() / 24)
}

// LocalDaysBetween is DaysUntil for instants (timestamp columns, time.Now):
// both are read as server-local dates first, so it's 0 on the same local day
// and 1 just after local midnight. Days overdue, "idle N days" and the like
// count this way.
func LocalDaysBetween(from, to time.Time) int {
	return DaysUntil(from.In(time.Local), to.In(time.Local))
}

// Parse accepts "2006-01-02" or an RFC 3339 timestamp (what a JS Date
// serializes to) and returns its Day. A timestamp's date is read in its own
// offset, so "2026-10-01T00:00:00+07:00" stays 1 October.
func Parse(s string) (time.Time, error) {
	if t, err := time.Parse(dateLayout, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return Day(t), nil
	}
	return time.Time{}, ErrInvalidDate
}

// ParseLocalDay is Parse for fields a server-local day is read from
// (Deal.ExpectedCloseDate, Quote.ValidityDate): a bare date is taken as-is,
// but a timestamp is read in server-local time rather than its own offset —
// the frontend stores a picked day as a JS Date, e.g.
// "2026-08-31T17:00:00.000Z" for 1 September in Bangkok.
func ParseLocalDay(s string) (day time.Time, ok bool) {
	if t, err := time.Parse(dateLayout, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return LocalDay(t), true
	}
	return time.Time{}, false
}

// ParseLocalMidnight reads a bare "2006-01-02" as server-local midnight —
// the instant a date filter on a timestamp column starts at (time.Parse
// would give UTC midnight, 07:00 in Bangkok).
func ParseLocalMidnight(s string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, s, time.Local)
}

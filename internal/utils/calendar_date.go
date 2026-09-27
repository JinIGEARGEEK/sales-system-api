package utils

import (
	"errors"
	"time"
)

// Calendar-day helpers — the one place day differences are computed.
// DaysUntil/CalendarDay/Today are for `type:date` columns (CustomerProduct.
// RenewalDate, Contract.EndDate); LocalDaysBetween is for timestamps. These carry no time of day, so they are
// handled as the Y-M-D the user picked, pinned to UTC midnight — the same
// shape Postgres hands a date column back in — and compared by calendar day,
// never by elapsed hours, so the server's TZ can't shift them by a day.

// ErrInvalidCalendarDate is returned by ParseCalendarDate.
var ErrInvalidCalendarDate = errors.New("must be a YYYY-MM-DD date or an RFC 3339 timestamp")

// ParseCalendarDate accepts "2006-01-02" or an RFC 3339 timestamp (what a
// JS Date serializes to) and returns that value's own Y-M-D at UTC midnight.
// For a timestamp the date is read in the timestamp's own offset, so
// "2026-10-01T00:00:00+07:00" stays 1 October.
func ParseCalendarDate(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return CalendarDay(t), nil
	}
	return time.Time{}, ErrInvalidCalendarDate
}

// CalendarDay drops t's time of day, keeping the Y-M-D as seen in t's own
// location, at UTC midnight.
func CalendarDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Today is the server-local calendar date (TZ, e.g. Asia/Bangkok) as a
// CalendarDay, for comparing against date columns.
func Today(now time.Time) time.Time {
	return CalendarDay(now.In(time.Local))
}

// DaysUntil counts whole calendar days from `from` to `to`, each read as
// the Y-M-D in its own location (CalendarDay) — for date values (date
// columns, parsed YYYY-MM-DD strings). Negative when `to` is earlier.
func DaysUntil(from, to time.Time) int {
	return int(CalendarDay(to).Sub(CalendarDay(from)).Hours() / 24)
}

// LocalDaysBetween is DaysUntil for instants (timestamp columns, time.Now):
// both are read as server-local (TZ, Asia/Bangkok) calendar dates first, so
// it's 0 on the same local day and 1 just after local midnight, and a
// timestamp stored as UTC midnight doesn't flip a day at 07:00 Bangkok.
// Days overdue, "idle N days" and the like all count this way.
func LocalDaysBetween(from, to time.Time) int {
	return DaysUntil(from.In(time.Local), to.In(time.Local))
}

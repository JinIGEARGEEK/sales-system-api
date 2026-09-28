package utils

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Calendar-day helpers — the one place day differences are computed, and
// where date_from/date_to filters are parsed (DateRange).
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

// ParseLocalDate reads a bare "2006-01-02" as server-local midnight (TZ,
// Asia/Bangkok) — the instant a date filter on a timestamp column starts
// at. time.Parse would give UTC midnight, 07:00 Bangkok, silently skipping
// the first seven hours of the day.
func ParseLocalDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, time.Local)
}

// ParseLocalCalendarDay is ParseCalendarDate for free-text date columns a
// server-local calendar day is read from (Deal.ExpectedCloseDate): a bare
// date is taken as-is, but an RFC 3339 timestamp is read in server-local
// time rather than its own offset — the frontend stores a picked day as a
// JS Date, e.g. "2026-08-31T17:00:00.000Z" for 1 September in Bangkok.
// Returns the day at UTC midnight, like CalendarDay; ok=false if unparseable.
func ParseLocalCalendarDay(s string) (day time.Time, ok bool) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return CalendarDay(t.In(time.Local)), true
	}
	return time.Time{}, false
}

// DateRange is a date_from/date_to filter on a timestamp column, both
// bounds inclusive server-local calendar days: From is date_from's local
// midnight, ToExclusive the local midnight after date_to, so date_to covers
// its whole day. A nil bound is open. Built by ParseDateRange.
type DateRange struct {
	From        *time.Time
	ToExclusive *time.Time
}

// ParseDateRange parses a date_from/date_to pair (YYYY-MM-DD each, "" for
// unbounded) into a DateRange. fromName/toName are the query params the
// values came from, so the 422 message/field errors name what the caller
// actually sent; fields is nil when the range is valid. A date_to before
// date_from is rejected too.
func ParseDateRange(fromName, fromValue, toName, toValue string) (r DateRange, msg string, fields map[string][]string) {
	parse := func(name, v string) (*time.Time, bool) {
		if v == "" {
			return nil, true
		}
		t, err := ParseLocalDate(v)
		if err != nil {
			msg, fields = name+" is invalid", map[string][]string{name: {"must be a valid YYYY-MM-DD date"}}
			return nil, false
		}
		return &t, true
	}
	from, ok := parse(fromName, fromValue)
	if !ok {
		return DateRange{}, msg, fields
	}
	to, ok := parse(toName, toValue)
	if !ok {
		return DateRange{}, msg, fields
	}
	if from != nil && to != nil && to.Before(*from) {
		return DateRange{}, toName + " is before " + fromName, map[string][]string{toName: {"must not be before " + fromName}}
	}
	r.From = from
	if to != nil {
		end := to.AddDate(0, 0, 1)
		r.ToExclusive = &end
	}
	return r, "", nil
}

// IsZero reports whether neither bound is set.
func (r DateRange) IsZero() bool {
	return r.From == nil && r.ToExclusive == nil
}

// Apply filters query to `column >= From AND column < ToExclusive`, for
// whichever bounds are set. Qualify column (deals.created_at) wherever the
// query joins another table — every table has created_at.
func (r DateRange) Apply(query *gorm.DB, column string) *gorm.DB {
	if r.From != nil {
		query = query.Where(column+" >= ?", *r.From)
	}
	if r.ToExclusive != nil {
		query = query.Where(column+" < ?", *r.ToExclusive)
	}
	return query
}

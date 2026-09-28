package utils

import (
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/calendar"
)

// DateRange is a date_from/date_to filter on a timestamp column, both
// bounds inclusive server-local calendar days: From is date_from's local
// midnight, ToExclusive the local midnight after date_to, so date_to covers
// its whole day. A nil bound is open. Built by ParseDateRange.
type DateRange struct {
	From        *time.Time
	ToExclusive *time.Time
}

// DateRangeError is a malformed or reversed date range: Message and Fields
// are the 422's message and field errors, naming the params actually sent.
type DateRangeError struct {
	Message string
	Fields  map[string][]string
}

func (e *DateRangeError) Error() string { return e.Message }

// ReversedDateRange is the error for a range whose end is before its start.
func ReversedDateRange(fromName, toName string) *DateRangeError {
	return &DateRangeError{
		Message: toName + " is before " + fromName,
		Fields:  map[string][]string{toName: {"must not be before " + fromName}},
	}
}

// ParseDateRange parses a date_from/date_to pair (YYYY-MM-DD each, "" for
// unbounded) into a DateRange. fromName/toName are the query params the
// values came from. A malformed or reversed range is a *DateRangeError.
func ParseDateRange(fromName, fromValue, toName, toValue string) (DateRange, error) {
	parse := func(name, v string) (*time.Time, error) {
		if v == "" {
			return nil, nil
		}
		t, err := calendar.ParseLocalMidnight(v)
		if err != nil {
			return nil, &DateRangeError{
				Message: name + " is invalid",
				Fields:  map[string][]string{name: {"must be a valid YYYY-MM-DD date"}},
			}
		}
		return &t, nil
	}
	from, err := parse(fromName, fromValue)
	if err != nil {
		return DateRange{}, err
	}
	to, err := parse(toName, toValue)
	if err != nil {
		return DateRange{}, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return DateRange{}, ReversedDateRange(fromName, toName)
	}
	r := DateRange{From: from}
	if to != nil {
		end := to.AddDate(0, 0, 1)
		r.ToExclusive = &end
	}
	return r, nil
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

// Package tempora implements the time type system and time-related function
// semantics for a query engine.
//
// The four core types are deliberately distinct:
//
//   - Timestamp:     an absolute instant on the UTC timeline. Its string
//     appearance changes with the session time zone, the
//     instant does not.
//   - LocalDateTime: a wall-clock date and time with no time zone attached.
//     It is not an instant and cannot be compared against a
//     Timestamp without an explicit, policy-driven conversion.
//   - Date:          a calendar date with no time-of-day or time zone.
//   - Interval:      a duration expressed as (months, days, nanoseconds),
//     because the three components follow different arithmetic
//     rules (calendar months, local calendar days, exact time).
//
// Time zone rules (offsets, DST transitions, historical changes) come from the
// IANA tzdata through the standard library's time package. Everything else —
// calendar arithmetic, parsing, formatting, conversion policy, grouping — is
// implemented in this package.
package tempora

import (
	"errors"
	"time"
)

// ErrInvalidValue is returned when a component is outside its legal range
// (month 13, day 31 in February, hour 24, leap-year violations, ...).
var ErrInvalidValue = errors.New("tempora: invalid date/time component")

// Timestamp is an absolute instant. It is always stored in UTC so that the
// same instant written differently in different zones compares equal.
type Timestamp struct {
	t time.Time // always normalized to UTC, monotonic clock stripped
}

// NewTimestamp builds the instant denoted by the given wall components as
// observed in loc. DST gap/overlap handling is not configurable here; use a
// Session.LocalToTimestamp when policies matter. This constructor uses the
// session-free default policy (gap forward, overlap first occurrence).
func NewTimestamp(year int, month time.Month, day, hour, min, sec, nsec int, loc *time.Location) (Timestamp, error) {
	if loc == nil {
		loc = time.UTC
	}
	ldt, err := NewLocalDateTime(year, month, day, hour, min, sec, nsec)
	if err != nil {
		return Timestamp{}, err
	}
	return DefaultSession(loc).LocalToTimestamp(ldt)
}

// TimestampFromTime wraps an existing time.Time, normalizing it to UTC.
func TimestampFromTime(t time.Time) Timestamp {
	return Timestamp{t: t.UTC()}
}

// TimestampFromUnix builds a Timestamp from Unix epoch seconds and nanoseconds.
func TimestampFromUnix(sec, nsec int64) Timestamp {
	return Timestamp{t: time.Unix(sec, nsec).UTC()}
}

// Time returns the instant as a UTC time.Time.
func (ts Timestamp) Time() time.Time { return ts.t }

// Unix returns seconds since the Unix epoch.
func (ts Timestamp) Unix() int64 { return ts.t.Unix() }

// UnixNano returns nanoseconds since the Unix epoch.
func (ts Timestamp) UnixNano() int64 { return ts.t.UnixNano() }

// IsZero reports whether the timestamp carries no instant.
func (ts Timestamp) IsZero() bool { return ts.t.IsZero() }

// Equal reports whether two timestamps denote the same instant, regardless of
// the time zone in which they were written. This is the predicate that join
// keys and comparisons must use.
func (ts Timestamp) Equal(other Timestamp) bool { return ts.t.Equal(other.t) }

// Before reports whether ts is strictly earlier than other.
func (ts Timestamp) Before(other Timestamp) bool { return ts.t.Before(other.t) }

// After reports whether ts is strictly later than other.
func (ts Timestamp) After(other Timestamp) bool { return ts.t.After(other.t) }

// Compare returns -1, 0, +1 in timestamp order.
func (ts Timestamp) Compare(other Timestamp) int {
	switch {
	case ts.t.Before(other.t):
		return -1
	case ts.t.Equal(other.t):
		return 0
	default:
		return 1
	}
}

// Sub returns the exact elapsed time between two instants as a nanosecond-only
// Interval. DST never affects this: it is pure instant arithmetic.
func (ts Timestamp) Sub(other Timestamp) Interval {
	return Interval{Nanoseconds: ts.t.Sub(other.t).Nanoseconds()}
}

// JoinKey is the canonical key for joins and grouping by instant: the same
// instant always produces the same key, in every time zone.
func (ts Timestamp) JoinKey() string {
	return strconvFormatInt(ts.t.UnixNano())
}

// LocalDateTime is a wall-clock date/time without a time zone. It follows the
// local calendar even across DST transitions: adding one day always advances
// the wall fields by exactly 24 wall hours, which may map to 23 or 25 real
// hours once converted to an instant.
type LocalDateTime struct {
	Year                       int
	Month                      time.Month
	Day                        int
	Hour, Minute, Second, Nsec int
}

// NewLocalDateTime validates every component strictly. 2023-02-29,
// 2024-02-30 and hour 24 are errors, not silent normalizations.
func NewLocalDateTime(year int, month time.Month, day, hour, min, sec, nsec int) (LocalDateTime, error) {
	ldt := LocalDateTime{year, month, day, hour, min, sec, nsec}
	if !ldt.Valid() {
		return LocalDateTime{}, ErrInvalidValue
	}
	return ldt, nil
}

// Valid checks the component ranges against the proleptic Gregorian calendar.
func (l LocalDateTime) Valid() bool {
	if l.Month < time.January || l.Month > time.December {
		return false
	}
	if l.Day < 1 || l.Day > daysInMonth(l.Year, l.Month) {
		return false
	}
	if l.Hour < 0 || l.Hour > 23 || l.Minute < 0 || l.Minute > 59 ||
		l.Second < 0 || l.Second > 59 || l.Nsec < 0 || l.Nsec > 999_999_999 {
		return false
	}
	return l.Year >= 0 && l.Year <= 9999
}

// Date drops the time-of-day component.
func (l LocalDateTime) Date() Date { return Date{l.Year, l.Month, l.Day} }

// CompareLocal orders two wall times by their calendar fields only.
func (l LocalDateTime) CompareLocal(o LocalDateTime) int {
	if c := l.Date().Compare(o.Date()); c != 0 {
		return c
	}
	aw := l.Hour*3600 + l.Minute*60 + l.Second
	bw := o.Hour*3600 + o.Minute*60 + o.Second
	switch {
	case aw < bw:
		return -1
	case aw > bw:
		return 1
	default:
		return compareInt(l.Nsec, o.Nsec)
	}
}

// Date is a calendar date.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// NewDate validates the date strictly.
func NewDate(year int, month time.Month, day int) (Date, error) {
	d := Date{year, month, day}
	if !d.Valid() {
		return Date{}, ErrInvalidValue
	}
	return d, nil
}

// Valid reports whether the date exists on the proleptic Gregorian calendar.
func (d Date) Valid() bool {
	if d.Month < time.January || d.Month > time.December || d.Year < 0 || d.Year > 9999 {
		return false
	}
	return d.Day >= 1 && d.Day <= daysInMonth(d.Year, d.Month)
}

// AtMidnight is the LocalDateTime 00:00:00 of this date.
func (d Date) AtMidnight() LocalDateTime {
	return LocalDateTime{Year: d.Year, Month: d.Month, Day: d.Day}
}

// Compare returns -1, 0, +1 in calendar order.
func (d Date) Compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return compareInt(d.Year, o.Year)
	case d.Month != o.Month:
		return compareInt(int(d.Month), int(o.Month))
	default:
		return compareInt(d.Day, o.Day)
	}
}

// AddDays returns the date n calendar days later (n may be negative).
func (d Date) AddDays(n int) Date {
	y, m, day := daysToCivil(civilToDays(d.Year, int(d.Month), d.Day) + n)
	return Date{y, time.Month(m), day}
}

// AddMonths returns the date n months later, clamping to month end:
// Jan 31 + 1 month -> Feb 28/29. The clamp has no memory, so Feb 28 + 1
// month -> Mar 28 (never Mar 3).
func (d Date) AddMonths(n int) Date {
	y, m, day := addMonthsClamped(d.Year, int(d.Month), d.Day, n)
	return Date{y, time.Month(m), day}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

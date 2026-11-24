package tempora

import (
	"errors"
	"math"
)

// Interval is a duration in three independent components:
//
//   - Months:      calendar months. Addition lands on the same day-of-month
//     in the target month, clamped to month end.
//   - Days:        local calendar days. On a DST day a "day" maps to 23 or 25
//     real hours when applied to an instant in a real zone.
//   - Nanoseconds: exact elapsed time. Applied to instants it is immune to
//     DST; applied to a LocalDateTime it is plain wall arithmetic.
//
// The components are never normalized into one another: 1 month is not a fixed
// number of days, and 1 day is not a fixed number of nanoseconds.
type Interval struct {
	Months      int
	Days        int
	Nanoseconds int64
}

// Common time units for the Nanoseconds component.
const (
	Nanosecond  int64 = 1
	Microsecond       = 1000 * Nanosecond
	Millisecond       = 1000 * Microsecond
	Second            = 1000 * Millisecond
	Minute            = 60 * Second
	Hour              = 60 * Minute
)

// NewInterval validates and constructs an interval.
func NewInterval(months, days int, nanoseconds int64) (Interval, error) {
	iv := Interval{months, days, nanoseconds}
	if !iv.Valid() {
		return Interval{}, ErrInvalidValue
	}
	return iv, nil
}

// Valid rejects the one value that cannot be negated safely. Months and days
// are intentionally unbounded; calendar math uses arbitrary-width int.
func (iv Interval) Valid() bool {
	return iv.Nanoseconds != math.MinInt64
}

// MonthsInterval / DaysInterval / HoursInterval are readable constructors.
func MonthsInterval(n int) Interval  { return Interval{Months: n} }
func DaysInterval(n int) Interval    { return Interval{Days: n} }
func HoursInterval(n int64) Interval { return Interval{Nanoseconds: n * Hour} }

// Add combines two intervals componentwise.
func (iv Interval) Add(o Interval) Interval {
	return Interval{
		Months:      iv.Months + o.Months,
		Days:        iv.Days + o.Days,
		Nanoseconds: iv.Nanoseconds + o.Nanoseconds,
	}
}

// Negate flips every component.
func (iv Interval) Negate() Interval {
	return Interval{Months: -iv.Months, Days: -iv.Days, Nanoseconds: -iv.Nanoseconds}
}

// IsZero reports whether all three components are zero.
func (iv Interval) IsZero() bool {
	return iv.Months == 0 && iv.Days == 0 && iv.Nanoseconds == 0
}

// ErrBadIntervalSyntax is returned by ParseInterval on malformed ISO-8601
// duration strings.
var ErrBadIntervalSyntax = errors.New("tempora: invalid interval syntax")

// ParseInterval parses ISO-8601 durations: PnYnMnDTnHnMnS, e.g.
// "P1M" (1 month), "P1D", "PT12H30M", "P1Y2M10DT2H", "PT0.5S".
// Weeks ("P2W") are accepted and converted to 7 days, but may not be mixed
// with other components. A fractional number is only allowed on seconds.
func ParseInterval(s string) (Interval, error) {
	if len(s) < 2 || s[0] != 'P' {
		return Interval{}, ErrBadIntervalSyntax
	}
	iv := Interval{}
	inTime := false
	seenWeek := false
	seenAny := false
	for i := 1; i < len(s); {
		if s[i] == 'T' {
			if inTime {
				return Interval{}, ErrBadIntervalSyntax
			}
			inTime = true
			i++
			continue
		}
		// Read one [digits][.fraction] token followed by a unit letter.
		numStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == numStart || i >= len(s) {
			return Interval{}, ErrBadIntervalSyntax
		}
		whole := parseInt64Digits(s[numStart:i])
		var frac int64
		fracDigits := 0
		if s[i] == '.' || s[i] == ',' {
			i++
			frac = 0
			fracDigits = 0
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				frac = frac*10 + int64(s[i]-'0')
				fracDigits++
				i++
			}
			if fracDigits == 0 || i >= len(s) {
				return Interval{}, ErrBadIntervalSyntax
			}
			for scale := fracDigits; scale < 9; scale++ {
				frac *= 10
			}
			for scale := fracDigits; scale > 9; scale-- {
				frac /= 10
			}
		}
		unit := s[i]
		i++
		seenAny = true
		// Fractions are only defined on seconds.
		if fracDigits > 0 && !(inTime && unit == 'S') {
			return Interval{}, ErrBadIntervalSyntax
		}
		switch {
		case !inTime && unit == 'Y':
			iv.Months += int(whole) * 12
		case !inTime && unit == 'M':
			iv.Months += int(whole)
		case !inTime && unit == 'W':
			seenWeek = true
			iv.Days += int(whole) * 7
		case !inTime && unit == 'D':
			iv.Days += int(whole)
		case inTime && unit == 'H':
			iv.Nanoseconds += whole * Hour
		case inTime && unit == 'M':
			iv.Nanoseconds += whole * Minute
		case inTime && unit == 'S':
			iv.Nanoseconds += whole*Second + frac
		default:
			return Interval{}, ErrBadIntervalSyntax
		}
	}
	if !seenAny {
		return Interval{}, ErrBadIntervalSyntax
	}
	if seenWeek && (iv.Months != 0 || iv.Nanoseconds != 0 || iv.Days%7 != 0 || iv.Days == 0) {
		return Interval{}, ErrBadIntervalSyntax
	}
	return iv, nil
}

func parseInt64Digits(s string) int64 {
	var v int64
	for _, c := range s {
		v = v*10 + int64(c-'0')
	}
	return v
}

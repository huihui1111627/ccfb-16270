package tempora

import "time"

// This file implements interval arithmetic. The crucial distinction:
//
//   - On Timestamp, months/days are applied in the SESSION zone's local
//     calendar and the result is re-resolved through the session's DST
//     policies, while nanoseconds move the instant itself and are DST-proof.
//   - On LocalDateTime, everything is pure wall arithmetic; DST is never
//     consulted because no instant exists yet.
//
// Applying order is always months first, then days, then nanoseconds.

// Add applies an interval to an instant using the session calendar.
//
//	ts + 1 month  : same wall day-of-month (clamped at month end), re-resolved
//	ts + 1 day    : wall fields advance 24h => 23 or 25 real hours on DST days
//	ts + N nanos  : exact instant shift, unaffected by DST
//
// Applying order is months first, then days, then nanoseconds.
func (s *Session) Add(ts Timestamp, iv Interval) (Timestamp, error) {
	if iv.Months == 0 && iv.Days == 0 {
		// Pure exact-time shift: never touches wall fields or DST policy.
		return TimestampFromTime(ts.t.Add(time.Duration(iv.Nanoseconds))), nil
	}
	l := s.TimestampToLocal(ts)
	if iv.Months != 0 {
		ny, nm, nd := addMonthsClamped(l.Year, int(l.Month), l.Day, iv.Months)
		l.Year, l.Month, l.Day = ny, nm, nd
	}
	if iv.Days != 0 {
		y, m, d := daysToCivil(civilToDays(l.Year, int(l.Month), l.Day) + iv.Days)
		l.Year, l.Month, l.Day = y, m, d
	}
	resolved, err := s.LocalToTimestamp(l)
	if err != nil {
		return Timestamp{}, err
	}
	if iv.Nanoseconds != 0 {
		return TimestampFromTime(resolved.t.Add(time.Duration(iv.Nanoseconds))), nil
	}
	return resolved, nil
}

// SubInterval subtracts an interval (equivalent to Add with the negation).
func (s *Session) Sub(ts Timestamp, iv Interval) (Timestamp, error) {
	return s.Add(ts, iv.Negate())
}

// AddLocal applies an interval to a wall time. Months and days move calendar
// fields; nanoseconds move the wall clock with day carry. No zone, no DST,
// no policy — this operation cannot fail on gap/overlap.
func AddLocal(l LocalDateTime, iv Interval) (LocalDateTime, error) {
	if !l.Valid() {
		return LocalDateTime{}, ErrInvalidValue
	}
	if iv.Months != 0 {
		ny, nm, nd := addMonthsClamped(l.Year, int(l.Month), l.Day, iv.Months)
		l.Year, l.Month, l.Day = ny, nm, nd
	}
	if iv.Days != 0 {
		y, m, d := daysToCivil(civilToDays(l.Year, int(l.Month), l.Day) + iv.Days)
		l.Year, l.Month, l.Day = y, m, d
	}
	if iv.Nanoseconds != 0 {
		l = addWallNanoseconds(l, iv.Nanoseconds)
	}
	return l, nil
}

// AddDate adds an interval to a pure date (only months and days are honored;
// nanoseconds are rejected because dates carry no time-of-day).
func AddDate(d Date, iv Interval) (Date, error) {
	if iv.Nanoseconds != 0 {
		return Date{}, ErrInvalidValue
	}
	if iv.Months != 0 {
		d = d.AddMonths(iv.Months)
	}
	if iv.Days != 0 {
		d = d.AddDays(iv.Days)
	}
	return d, nil
}

// DateDiff returns the whole-day distance between two dates (a - b).
func DateDiff(a, b Date) int {
	return civilToDays(a.Year, int(a.Month), a.Day) -
		civilToDays(b.Year, int(b.Month), b.Day)
}

// LocalDiff returns the exact wall-clock difference between two local times
// as a nanosecond-only interval.
func LocalDiff(a, b LocalDateTime) Interval {
	da := civilToDays(a.Year, int(a.Month), a.Day)
	db := civilToDays(b.Year, int(b.Month), b.Day)
	sa := int64(da)*86400 + int64(a.Hour)*3600 + int64(a.Minute)*60 + int64(a.Second)
	sb := int64(db)*86400 + int64(b.Hour)*3600 + int64(b.Minute)*60 + int64(b.Second)
	return Interval{Nanoseconds: (sa-sb)*1e9 + int64(a.Nsec-b.Nsec)}
}

package tempora

import (
	"strconv"
	"time"
)

// This file implements the proleptic Gregorian calendar arithmetic from
// scratch (the Howard Hinnant civil-from-days / days-from-civil algorithms).
// No time-zone or DST behavior lives here: these are pure calendar rules.

// isLeap reports whether y is a Gregorian leap year.
func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

// daysInMonth returns the number of days in the given month of year y.
func daysInMonth(y int, m time.Month) int {
	switch m {
	case time.January, time.March, time.May, time.July, time.August, time.October, time.December:
		return 31
	case time.April, time.June, time.September, time.November:
		return 30
	case time.February:
		if isLeap(y) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

// civilToDays maps a civil date to a serial day number (1970-01-01 == 0).
// Algorithm: Howard Hinnant, "chrono-compatible Low-Level Date Algorithms".
func civilToDays(y, m, d int) int {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400 // [0, 399]
	mp := m
	if mp > 2 {
		mp -= 3
	} else {
		mp += 9
	} // [0, 11]
	doy := (153*mp+2)/5 + d - 1 // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// daysToCivil is the inverse of civilToDays.
func daysToCivil(z int) (y int, m time.Month, d int) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097 // [0, 146096]
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153 // [0, 11]
	d = doy - (153*mp+2)/5 + 1
	mi := mp + 3 // [1, 15]
	if mi > 12 {
		mi -= 12
	}
	if mi <= 2 {
		y++
	}
	return y, time.Month(mi), d
}

// floorDiv returns the floor of a/b for b > 0 (Go's / truncates toward zero).
func floorDiv(a, b int) int {
	q := a / b
	if r := a % b; r != 0 && r < 0 {
		q--
	}
	return q
}

// addMonthsClamped adds n calendar months and clamps the day to the last legal
// day of the target month. Deterministic: Jan 31 +1 = Feb 28; Feb 28 +1 =
// Mar 28 — the clamp never remembers an "original" day.
func addMonthsClamped(y, m, d, n int) (int, time.Month, int) {
	total := y*12 + (m - 1) + n // zero-based absolute month index
	ny, nm0 := floorDiv(total, 12), 0
	nm0 = total - ny*12
	nm := time.Month(nm0 + 1)
	if last := daysInMonth(ny, nm); d > last {
		d = last
	}
	return ny, nm, d
}

// addWallNanoseconds performs pure wall-clock arithmetic on a LocalDateTime:
// nanoseconds are folded into the time-of-day and carried into the calendar
// day count. DST cannot matter here because no instant is involved.
func addWallNanoseconds(l LocalDateTime, n int64) LocalDateTime {
	sod := int64(l.Hour)*3600 + int64(l.Minute)*60 + int64(l.Second)
	total := sod*1e9 + int64(l.Nsec) + n
	dayDelta := floorDiv64(total, 86400e9)
	rem := total - dayDelta*86400e9
	y, m, d := daysToCivil(civilToDays(l.Year, int(l.Month), l.Day) + int(dayDelta))
	secs := rem / 1e9
	return LocalDateTime{
		Year: y, Month: m, Day: d,
		Hour: int(secs / 3600), Minute: int((secs % 3600) / 60),
		Second: int(secs % 60), Nsec: int(rem % 1e9),
	}
}

func floorDiv64(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && r < 0 {
		q--
	}
	return q
}

// strconvFormatInt is the canonical instant join-key encoding (UTC nanos).
func strconvFormatInt(v int64) string { return strconv.FormatInt(v, 10) }

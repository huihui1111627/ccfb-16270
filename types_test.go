package tempora

import (
	"testing"
	"time"
)

// Embedded tzdata makes every result independent of the host machine's
// zoneinfo version. The standard library time package is the authoritative
// reference throughout this suite: both code and tests read the same IANA
// rules, while all arithmetic under test is our own implementation.
import _ "time/tzdata"

func TestCivilRoundTrip(t *testing.T) {
	cases := []struct{ y, m, d int }{
		{1970, 1, 1}, {1969, 12, 31}, {2000, 2, 29}, {1900, 2, 28},
		{2100, 2, 28}, {2024, 12, 31}, {1, 1, 1}, {9999, 12, 31},
		{2026, 11, 25}, {1582, 10, 15},
	}
	for _, c := range cases {
		z := civilToDays(c.y, c.m, c.d)
		gy, gm, gd := daysToCivil(z)
		if gy != c.y || int(gm) != c.m || gd != c.d {
			t.Fatalf("round trip %v -> %d -> %d-%d-%d", c, z, gy, gm, gd)
		}
	}
}

func TestWeekdayMatchesStdlib(t *testing.T) {
	loc := time.UTC
	cur := time.Date(2020, 1, 1, 0, 0, 0, 0, loc)
	end := time.Date(2030, 1, 1, 0, 0, 0, 0, loc)
	for cur.Before(end) {
		got := weekday(civilToDays(cur.Year(), int(cur.Month()), cur.Day()))
		want := (int(cur.Weekday()) + 6) % 7 // Sunday=7 -> Monday=0
		if got != want {
			t.Fatalf("weekday %v: got %d want %d", cur.Format("2006-01-02"), got, want)
		}
		cur = cur.AddDate(0, 0, 1)
	}
}

func TestLeapAndInvalidDates(t *testing.T) {
	valid := []Date{
		{2024, time.February, 29}, {2000, time.February, 29},
		{2023, time.February, 28}, {2024, time.April, 30},
	}
	for _, d := range valid {
		if !d.Valid() {
			t.Errorf("%v should be valid", d)
		}
	}
	invalid := []Date{
		{2023, time.February, 29}, {1900, time.February, 29},
		{2024, time.February, 30}, {2024, time.April, 31},
		{2024, 13, 1}, {2024, time.January, 0},
	}
	for _, d := range invalid {
		if d.Valid() {
			t.Errorf("%v should be invalid", d)
		}
		if _, err := NewDate(d.Year, d.Month, d.Day); err == nil {
			t.Errorf("NewDate(%v) expected error", d)
		}
	}
	bad := []LocalDateTime{
		{2024, time.March, 10, 24, 0, 0, 0},
		{2024, time.March, 10, 12, 60, 0, 0},
		{2024, time.March, 10, 12, 0, 60, 0},
		{2024, time.March, 10, 12, 0, 0, 1_000_000_000},
	}
	for _, l := range bad {
		if l.Valid() {
			t.Errorf("%v should be invalid", l)
		}
	}
}

func TestAddMonthsClamp(t *testing.T) {
	cases := []struct {
		y, m, d, n int
		wy, wm, wd int
	}{
		{2024, 1, 31, 1, 2024, 2, 29},  // leap Feb clamp
		{2023, 1, 31, 1, 2023, 2, 28},  // non-leap Feb clamp
		{2023, 2, 28, 1, 2023, 3, 28},  // no memory of day 31
		{2024, 3, 31, -1, 2024, 2, 29}, // backward clamp
		{2023, 12, 15, 1, 2024, 1, 15}, // year rollover
		{2024, 1, 15, 12, 2025, 1, 15},
		{2024, 1, 31, 3, 2024, 4, 30}, // Apr has 30 days
		{2024, 11, 30, 3, 2025, 2, 28},
	}
	for _, c := range cases {
		y, m, d := addMonthsClamped(c.y, c.m, c.d, c.n)
		if y != c.wy || int(m) != c.wm || d != c.wd {
			t.Errorf("addMonthsClamped(%d-%02d-%02d,%d) = %d-%02d-%02d want %d-%02d-%02d",
				c.y, c.m, c.d, c.n, y, m, d, c.wy, c.wm, c.wd)
		}
	}
}

// TestMonthEndBusinessCase is the exact bug from the report: Jan 31 + 1 month.
func TestMonthEndBusinessCase(t *testing.T) {
	jan31 := Date{2023, time.January, 31}
	feb28 := jan31.AddMonths(1)
	mar28 := feb28.AddMonths(1)
	if feb28 != (Date{2023, time.February, 28}) {
		t.Fatalf("Jan31+1mo = %v, want Feb 28", feb28)
	}
	if mar28 != (Date{2023, time.March, 28}) {
		t.Fatalf("Feb28+1mo = %v, want Mar 28 (deterministic, no Mar 3)", mar28)
	}
}

func TestParseInterval(t *testing.T) {
	cases := []struct {
		s    string
		want Interval
	}{
		{"P1Y", Interval{Months: 12}},
		{"P1M", Interval{Months: 1}},
		{"P1D", Interval{Days: 1}},
		{"PT1H", Interval{Nanoseconds: Hour}},
		{"PT90M", Interval{Nanoseconds: 90 * Minute}},
		{"P1Y2M10DT2H30M", Interval{Months: 14, Days: 10, Nanoseconds: 2*Hour + 30*Minute}},
		{"PT0.5S", Interval{Nanoseconds: 500 * Millisecond}},
		{"PT1.250S", Interval{Nanoseconds: Second + 250*Millisecond}},
		{"P2W", Interval{Days: 14}},
	}
	for _, c := range cases {
		got, err := ParseInterval(c.s)
		if err != nil {
			t.Fatalf("ParseInterval(%q): %v", c.s, err)
		}
		if got != c.want {
			t.Errorf("ParseInterval(%q) = %+v, want %+v", c.s, got, c.want)
		}
	}
	bad := []string{"", "1D", "P", "PT", "P1H", "PT1Y", "P1.5D", "P2W1D", "xyz", "PMM"}
	for _, s := range bad {
		if _, err := ParseInterval(s); err == nil {
			t.Errorf("ParseInterval(%q) expected error", s)
		}
	}
}

func TestDateDiff(t *testing.T) {
	a, _ := NewDate(2024, time.March, 10)
	b, _ := NewDate(2023, time.March, 10)
	if got := DateDiff(a, b); got != 366 { // 2024 leap day included
		t.Errorf("DateDiff = %d, want 366", got)
	}
	if got := DateDiff(b, a); got != -366 {
		t.Errorf("reverse DateDiff = %d, want -366", got)
	}
}

func TestLocalWallArithmetic(t *testing.T) {
	l, _ := NewLocalDateTime(2024, time.March, 9, 23, 30, 0, 0)
	got := addWallNanoseconds(l, 90*Minute)
	want := LocalDateTime{2024, time.March, 10, 1, 0, 0, 0}
	if got != want {
		t.Fatalf("wall add = %+v, want %+v", got, want)
	}
	// Negative carry across a month boundary.
	l2, _ := NewLocalDateTime(2024, time.March, 1, 0, 0, 0, 0)
	got2 := addWallNanoseconds(l2, -Second)
	want2 := LocalDateTime{2024, time.February, 29, 23, 59, 59, 0}
	if got2 != want2 {
		t.Fatalf("negative wall add = %+v, want %+v", got2, want2)
	}
}

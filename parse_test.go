package tempora

import (
	"strings"
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	// Dates.
	d, err := ParseDate("2024-02-29")
	if err != nil || d != (Date{2024, time.February, 29}) {
		t.Fatalf("date parse: %v %v", d, err)
	}
	if _, err = ParseDate("2024/03/10"); err != nil {
		t.Fatalf("slash date: %v", err)
	}

	// Local date/times.
	l, err := ParseLocalDateTime("2024-03-10T02:30:00")
	if err != nil || l != (LocalDateTime{2024, time.March, 10, 2, 30, 0, 0}) {
		t.Fatalf("local parse: %+v %v", l, err)
	}
	l2, err := ParseLocalDateTime("2024-03-10 02:30:00.123456789")
	if err != nil || l2.Nsec != 123456789 {
		t.Fatalf("fraction parse: %+v %v", l2, err)
	}

	// Timestamps: Z, numeric offsets in all three shapes, IANA name.
	ts1, err := ParseTimestamp("2024-03-10T07:00:00Z")
	if err != nil || ts1.Unix() != time.Date(2024, 3, 10, 7, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("Z parse: %v %v", ts1, err)
	}
	for _, s := range []string{
		"2024-03-10T15:00:00+08:00",
		"2024-03-10T15:00:00+0800",
		"2024-03-10T15:00:00+08",
	} {
		ts, err := ParseTimestamp(s)
		if err != nil || ts.Unix() != ts1.Unix() {
			t.Fatalf("offset %q => %v err=%v, want same instant as Z form", s, ts, err)
		}
	}
	tsNY, err := ParseTimestamp("2024-03-10T03:00:00[America/New_York]")
	if err != nil || tsNY.Unix() != ts1.Unix() {
		t.Fatalf("IANA bracket parse: %v %v", tsNY, err)
	}
	tsNY2, err := ParseTimestamp("2024-03-10T03:00:00 America/New_York")
	if err != nil || tsNY2.Unix() != ts1.Unix() {
		t.Fatalf("IANA space parse: %v %v", tsNY2, err)
	}
}

func TestParseRejects(t *testing.T) {
	badDates := []string{
		"2023-02-29", "2024-02-30", "2024-13-01", "2024-00-10",
		"2024-04-31", "1900-02-29", "not-a-date", "24-03-10",
	}
	for _, s := range badDates {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q) should fail", s)
		}
	}
	badLocal := []string{
		"2024-03-10T24:00:00", "2024-03-10T12:60:00",
		"2024-03-10T12:00:61", "2024-03-10", // date alone is not a local datetime
		"2024-03-10T02:30:00Z",           // zone not allowed on local type
		"2024-03-10T02:30:00.1234567890", // > nanosecond
	}
	for _, s := range badLocal {
		if _, err := ParseLocalDateTime(s); err == nil {
			t.Errorf("ParseLocalDateTime(%q) should fail", s)
		}
	}
	badTS := []string{
		"2024-03-10T02:30:00",          // no zone
		"2024-03-10T02:30:00[No/Such]", // unknown zone
		"2024-03-10T02:30:00+25:00",    // offset range left permissive? ensure shape fails clean
		"2024-03-10T02:30:00CST",       // ambiguous abbreviation rejected
	}
	for _, s := range badTS {
		if _, err := ParseTimestamp(s); err == nil {
			t.Errorf("ParseTimestamp(%q) should fail", s)
		}
	}
}

func TestParseTimestampAtOverlapHonorsPolicy(t *testing.T) {
	sess := SessionFromLocation(mustZone(t, "America/New_York")).
		WithOverlapPolicy(OverlapReject)
	if _, err := ParseTimestampSession("2024-11-03T01:30:00[America/New_York]", sess); err != ErrOverlap {
		t.Fatalf("reject policy err = %v, want ErrOverlap", err)
	}
	first, err := ParseTimestampSession("2024-11-03T01:30:00[America/New_York]",
		SessionFromLocation(mustZone(t, "America/New_York")).WithOverlapPolicy(OverlapFirst))
	if err != nil {
		t.Fatal(err)
	}
	last, err := ParseTimestampSession("2024-11-03T01:30:00[America/New_York]",
		SessionFromLocation(mustZone(t, "America/New_York")).WithOverlapPolicy(OverlapLast))
	if err != nil {
		t.Fatal(err)
	}
	if last.Time().Sub(first.Time()) != time.Hour {
		t.Fatalf("overlap occurrences not 1h apart")
	}
}

func TestFormatRoundTrip(t *testing.T) {
	l, _ := ParseLocalDateTime("2024-03-10T02:30:45.25")
	out, err := FormatLocal(l, "%Y-%m-%d %H:%M:%S.%f")
	if err != nil {
		t.Fatal(err)
	}
	if want := "2024-03-10 02:30:45.250000000"; out != want {
		t.Fatalf("format = %q, want %q", out, want)
	}
	if iso := ISOLocal(l); iso != "2024-03-10T02:30:45.250000000" {
		t.Fatalf("ISO local = %q", iso)
	}
	if iso := ISODate(Date{2024, time.March, 10}); iso != "2024-03-10" {
		t.Fatalf("ISO date = %q", iso)
	}

	sess, _ := NewSession("America/New_York")
	ts, _ := sess.LocalToTimestamp(ldt(2024, time.March, 15, 8, 0))
	iso := sess.ISOTimestamp(ts)
	if iso != "2024-03-15T08:00:00-04:00" {
		t.Fatalf("ISO ts = %q", iso)
	}
	named, err := sess.FormatTimestamp(ts, "%A %d %B %Y, %H:%M %Z (%:z)")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(named, "Friday") || !strings.Contains(named, "EDT") ||
		!strings.Contains(named, "-04:00") {
		t.Fatalf("named format = %q", named)
	}
	// Round trip through our own parser on the offset form.
	back, err := ParseTimestamp(iso)
	if err != nil || !back.Equal(ts) {
		t.Fatalf("round trip %q -> %v err=%v", iso, back, err)
	}
	// Zone tokens must be rejected for zone-less types.
	if _, err := FormatLocal(l, "%z"); err == nil {
		t.Fatal("FormatLocal with %z should fail")
	}
}

func TestTransitionFinder(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	trs, err := sess.TransitionsOn(Date{2024, time.March, 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(trs) != 1 {
		t.Fatalf("transitions = %d, want 1", len(trs))
	}
	tr := trs[0]
	wantUTC := time.Date(2024, time.March, 10, 7, 0, 0, 0, time.UTC)
	if !tr.AtUTC.Time().Equal(wantUTC) {
		t.Fatalf("transition at %v, want %v", tr.AtUTC.Time(), wantUTC)
	}
	if tr.GapWindow == nil || tr.GapWindow.From != ldt(2024, time.March, 10, 2, 0) ||
		tr.GapWindow.To != ldt(2024, time.March, 10, 3, 0) {
		t.Fatalf("gap window = %+v", tr.GapWindow)
	}
	// A normal day has no transitions.
	none, err := sess.TransitionsOn(Date{2024, time.June, 15})
	if err != nil || len(none) != 0 {
		t.Fatalf("June transitions = %d err=%v", len(none), err)
	}
	// Fall-back day reports an overlap window 01:00 -> 02:00.
	fb, _ := sess.TransitionsOn(Date{2024, time.November, 3})
	if len(fb) != 1 || fb[0].OverlapWindow == nil ||
		fb[0].OverlapWindow.From != ldt(2024, time.November, 3, 1, 0) ||
		fb[0].OverlapWindow.To != ldt(2024, time.November, 3, 2, 0) {
		t.Fatalf("fall transition = %+v", fb)
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestLocalTimeline(t *testing.T) {
	ny, _ := NewSession("America/New_York")
	hours, err := ny.LocalTimeline(Date{2024, time.March, 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 24 {
		t.Fatalf("spring timeline cells = %d, want 24", len(hours))
	}
	var gaps, overlaps int
	for _, h := range hours {
		switch h.Kind {
		case WallGap:
			gaps++
			if !h.Start.IsZero() {
				t.Fatal("gap cell must carry no instant")
			}
		case WallOverlap:
			overlaps++
		}
	}
	if gaps != 1 || overlaps != 0 {
		t.Fatalf("spring gaps=%d overlaps=%d, want 1/0", gaps, overlaps)
	}

	fh, _ := ny.LocalTimeline(Date{2024, time.November, 3})
	if len(fh) != 24 {
		t.Fatalf("fall timeline cells = %d, want 24 labels", len(fh))
	}
	if ov := countKind(fh, WallOverlap); ov != 1 {
		t.Fatalf("fall overlap labels = %d, want 1 (the 01:00 cell)", ov)
	}

	// Whole missing day: every one of the 24 labels must still be reported.
	apia, _ := NewSession("Pacific/Apia")
	missing, err := apia.LocalTimeline(Date{2011, time.December, 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 24 || countKind(missing, WallGap) != 24 {
		t.Fatalf("Apia missing day: %d cells, gaps=%d, want 24/24", len(missing), countKind(missing, WallGap))
	}
}

func countKind(hs []TimelineHour, k WallKind) int {
	n := 0
	for _, h := range hs {
		if h.Kind == k {
			n++
		}
	}
	return n
}

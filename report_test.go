package tempora

import (
	"testing"
	"time"
)

func TestHourlySpineSpringForward(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	dayStart, _ := sess.LocalToTimestamp(ldt(2024, time.March, 10, 0, 0))
	dayEnd, _ := sess.LocalToTimestamp(ldt(2024, time.March, 11, 0, 0))
	spine, err := sess.HourlySpine(dayStart, dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	// 24 wall labels, one of them a gap => 23 real buckets + 1 gap marker.
	if len(spine) != 24 {
		t.Fatalf("spring spine rows = %d, want 24 (23 real + 1 gap)", len(spine))
	}
	var gaps, real int
	for _, b := range spine {
		switch b.Kind {
		case BucketGap:
			gaps++
			if !b.Start.IsZero() || !b.End.IsZero() {
				t.Fatalf("gap bucket must carry no instant: %+v", b)
			}
			if b.Wall != ldt(2024, time.March, 10, 2, 0) {
				t.Fatalf("gap at wrong wall label: %v", b.Wall)
			}
		default:
			real++
			if b.RealSpan != time.Hour {
				t.Fatalf("non-gap real span = %v, want 1h", b.RealSpan)
			}
		}
	}
	if gaps != 1 || real != 23 {
		t.Fatalf("gaps=%d real=%d, want 1/23", gaps, real)
	}
	// Total real elapsed time must be 23 hours, never 24.
	total := time.Duration(0)
	for _, b := range spine {
		if b.Kind != BucketGap {
			total += b.RealSpan
		}
	}
	if total != 23*time.Hour {
		t.Fatalf("spring day elapsed = %v, want 23h", total)
	}
}

func TestHourlySpineFallBack(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	dayStart, _ := sess.LocalToTimestamp(ldt(2024, time.November, 3, 0, 0))
	dayEnd, _ := sess.LocalToTimestamp(ldt(2024, time.November, 4, 0, 0))
	spine, err := sess.HourlySpine(dayStart, dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	// 24 wall labels; the repeated 01:00 label appears twice => 25 buckets.
	if len(spine) != 25 {
		t.Fatalf("fall spine rows = %d, want 25 (two occurrences of 01:00)", len(spine))
	}
	var first, last, normal int
	for _, b := range spine {
		switch b.Kind {
		case BucketOverlapFirst:
			first++
			if b.Wall != ldt(2024, time.November, 3, 1, 0) {
				t.Fatalf("overlap first at wrong label: %v", b.Wall)
			}
		case BucketOverlapLast:
			last++
			if b.Wall != ldt(2024, time.November, 3, 1, 0) {
				t.Fatalf("overlap last at wrong label: %v", b.Wall)
			}
		case BucketNormal:
			normal++
		default:
			t.Fatalf("unexpected gap on fall-back day")
		}
	}
	if first != 1 || last != 1 || normal != 23 {
		t.Fatalf("first=%d last=%d normal=%d, want 1/1/23", first, last, normal)
	}
	total := time.Duration(0)
	for _, b := range spine {
		total += b.RealSpan
	}
	if total != 25*time.Hour {
		t.Fatalf("fall day elapsed = %v, want 25h", total)
	}
	// The two overlap buckets must be contiguous and chronologically ordered.
	var ov []HourBucket
	for _, b := range spine {
		if b.Kind == BucketOverlapFirst || b.Kind == BucketOverlapLast {
			ov = append(ov, b)
		}
	}
	if len(ov) != 2 || !ov[0].End.Equal(ov[1].Start) || !ov[0].Start.Before(ov[1].Start) {
		t.Fatalf("overlap buckets not contiguous/ordered: %+v", ov)
	}
}

func TestAggregateHourlyReport(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	dayStart, _ := sess.LocalToTimestamp(ldt(2024, time.November, 3, 0, 0))
	dayEnd, _ := sess.LocalToTimestamp(ldt(2024, time.November, 4, 0, 0))

	// One event per real hour (25 events on the fall-back day), value 10 each.
	type E = struct {
		TS Timestamp
		V  int64
	}
	var events []E
	spine, _ := sess.HourlySpine(dayStart, dayEnd)
	for _, b := range spine {
		// Mid-bucket instant.
		mid := TimestampFromTime(b.Start.t.Add(30 * time.Minute))
		events = append(events, E{mid, 10})
	}
	rows, err := sess.AggregateHourly(dayStart, dayEnd, events)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 25 {
		t.Fatalf("report rows = %d, want 25", len(rows))
	}
	var count int64
	var sum int64
	for _, r := range rows {
		if !r.Has {
			t.Fatalf("every fall-back bucket should have an event: %+v", r)
		}
		count += r.Count
		sum += r.Sum
	}
	if count != 25 || sum != 250 {
		t.Fatalf("count=%d sum=%d, want 25/250 (no hour merged away)", count, sum)
	}

	// Spring day: gap row must stay empty and MUST NOT be fabricated.
	sDay, _ := sess.LocalToTimestamp(ldt(2024, time.March, 10, 0, 0))
	eDay, _ := sess.LocalToTimestamp(ldt(2024, time.March, 11, 0, 0))
	srows, err := sess.AggregateHourly[int64](sDay, eDay, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(srows) != 24 {
		t.Fatalf("spring report rows = %d, want 24", len(srows))
	}
	for _, r := range srows {
		if r.Kind == BucketGap && (r.Has || r.Count != 0 || r.Sum != 0) {
			t.Fatalf("gap bucket fabricated data: %+v", r)
		}
	}
}

func TestTruncation(t *testing.T) {
	ny, _ := NewSession("America/New_York")
	london, _ := NewSession("Europe/London")
	ts, _ := ny.LocalToTimestamp(ldt(2024, time.March, 15, 13, 47))
	h, err := ny.Trunc(ts, TruncHour)
	if err != nil {
		t.Fatal(err)
	}
	if got := ny.TimestampToLocal(h); got != ldt(2024, time.March, 15, 13, 0) {
		t.Fatalf("hour trunc = %v", got)
	}
	d, _ := ny.Trunc(ts, TruncDay)
	if got := ny.TimestampToLocal(d); got.Hour != 0 || got.Day != 15 {
		t.Fatalf("day trunc = %v", got)
	}
	// Same instant, different session: truncation differs by zone but the
	// results remain comparable instants.
	dl, _ := london.Trunc(ts, TruncDay)
	if dl.Equal(d) {
		t.Fatalf("day boundaries in NY and London should be different instants")
	}
	// Week trunc: 2024-03-15 is a Friday -> Monday 2024-03-11.
	w, _ := ny.Trunc(ts, TruncWeek)
	if got := ny.TimestampToLocal(w); !(got.Year == 2024 && got.Month == time.March && got.Day == 11) {
		t.Fatalf("week trunc = %v, want Mon 2024-03-11", got)
	}
	mth, _ := ny.Trunc(ts, TruncMonth)
	if got := ny.TimestampToLocal(mth); got.Day != 1 || got.Month != time.March {
		t.Fatalf("month trunc = %v", got)
	}
}

func TestDailySpine(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	s, _ := sess.LocalToTimestamp(ldt(2024, time.March, 8, 12, 0))
	e, _ := sess.LocalToTimestamp(ldt(2024, time.March, 12, 12, 0))
	days, err := sess.DailySpine(s, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 5 {
		t.Fatalf("daily rows = %d, want 5", len(days))
	}
	for i, dy := range days {
		if dy.Wall.Day != 8+i {
			t.Fatalf("day %d label = %v", i, dy.Wall)
		}
	}
}

func TestCrossZoneJoinKey(t *testing.T) {
	ny, _ := NewSession("America/New_York")
	berlin, _ := NewSession("Europe/Berlin")
	a, _ := ny.LocalToTimestamp(ldt(2024, time.June, 1, 12, 0))     // 16:00 UTC
	b, _ := berlin.LocalToTimestamp(ldt(2024, time.June, 1, 18, 0)) // 16:00 UTC
	if a.JoinKey() != b.JoinKey() || a.Compare(b) != 0 {
		t.Fatalf("join keys differ for same instant: %q vs %q", a.JoinKey(), b.JoinKey())
	}
	// Sorting a mixed-zone list yields UTC timeline order.
	c, _ := berlin.LocalToTimestamp(ldt(2024, time.June, 1, 17, 0)) // 15:00 UTC
	if !(c.Before(a) && a.After(c)) {
		t.Fatal("ordering across zones wrong")
	}
}

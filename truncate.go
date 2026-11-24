package tempora

import (
	"time"
)

// TruncUnit selects a calendar granularity. All truncation is performed in the
// SESSION zone's local calendar, never in UTC.
type TruncUnit int

const (
	TruncHour TruncUnit = iota
	TruncDay
	TruncWeek // ISO week, Monday start
	TruncMonth
	TruncYear
)

// Trunc snaps an instant down to the bucket boundary in the session zone.
// Boundaries are real instants: a truncated instant never lands inside a gap.
func (s *Session) Trunc(ts Timestamp, unit TruncUnit) (Timestamp, error) {
	l := s.TimestampToLocal(ts)
	l = truncLocal(l, unit)
	return s.LocalToTimestamp(l)
}

// TruncLocal snaps a wall time down to a local calendar boundary. Pure field
// arithmetic; no zone involvement.
func TruncLocal(l LocalDateTime, unit TruncUnit) LocalDateTime {
	return truncLocal(l, unit)
}

func truncLocal(l LocalDateTime, unit TruncUnit) LocalDateTime {
	out := l
	switch unit {
	case TruncHour:
		out.Minute, out.Second, out.Nsec = 0, 0, 0
	case TruncDay:
		out.Hour, out.Minute, out.Second, out.Nsec = 0, 0, 0, 0
	case TruncWeek:
		out.Hour, out.Minute, out.Second, out.Nsec = 0, 0, 0, 0
		wd := weekday(civilToDays(l.Year, int(l.Month), l.Day)) // Monday=0 ... Sunday=6
		y, m, d := daysToCivil(civilToDays(l.Year, int(l.Month), l.Day) - wd)
		out.Year, out.Month, out.Day = y, m, d
	case TruncMonth:
		out.Day, out.Hour, out.Minute, out.Second, out.Nsec = 1, 0, 0, 0, 0
	case TruncYear:
		out.Month, out.Day, out.Hour, out.Minute, out.Second, out.Nsec = time.January, 1, 0, 0, 0, 0
	}
	return out
}

// weekday maps a serial day to Monday=0 ... Sunday=6 (1970-01-01 was Thursday).
func weekday(serial int) int {
	w := (serial + 3) % 7 // Thursday(3) on day 0 => Monday=0
	if w < 0 {
		w += 7
	}
	return w
}

// BucketKind labels each row of an hourly spine.
type BucketKind int

const (
	BucketNormal       BucketKind = iota // ordinary wall hour, one instant start
	BucketGap                            // wall hour skipped by spring forward; no instant, never fabricated
	BucketOverlapFirst                   // first occurrence of a repeated fall-back hour
	BucketOverlapLast                    // second occurrence of a repeated fall-back hour
)

func (k BucketKind) String() string {
	switch k {
	case BucketGap:
		return "gap"
	case BucketOverlapFirst:
		return "overlap-first"
	case BucketOverlapLast:
		return "overlap-last"
	default:
		return "normal"
	}
}

// HourBucket is one row of the hourly report spine.
type HourBucket struct {
	Wall     LocalDateTime // hour label in session zone, :00:00
	Kind     BucketKind
	Start    Timestamp     // bucket start instant; zero for gap buckets
	End      Timestamp     // exclusive end; zero for gap buckets
	Offset   int           // offset seconds east of UTC at Start
	WallSpan time.Duration // wall-clock length (always 1h here; kept for symmetry)
	RealSpan time.Duration // real elapsed time Start->End (1h, or 2h for fall-back first-occurrence semantics)
}

// HourlySpine enumerates every wall-clock hour bucket between two instants
// (start inclusive, end exclusive), in the session zone.
//
// The spine is the complete local calendar: 24 wall labels on every day.
// On a spring-forward day exactly one bucket is BucketGap — its Start is zero
// and callers MUST leave its measures null rather than inventing data. On a
// fall-back day the repeated hour appears as two buckets (first/last
// occurrence), so an event never loses its hour and the two are never merged.
func (s *Session) HourlySpine(start, end Timestamp) ([]HourBucket, error) {
	if !start.Before(end) {
		return nil, nil
	}
	startLocal := truncLocal(s.TimestampToLocal(start), TruncHour)
	endLocal := truncLocal(s.TimestampToLocal(end), TruncHour)

	var rows []HourBucket
	cur := startLocal
	for cur.CompareLocal(endLocal) < 0 {
		r, err := s.ResolveWall(cur)
		if err != nil {
			return nil, err
		}
		nextWall := addWallNanoseconds(cur, int64(Hour))
		switch r.Kind {
		case WallNormal:
			nr, err := s.ResolveWall(nextWall)
			if err != nil {
				return nil, err
			}
			rows = append(rows, HourBucket{
				Wall: cur, Kind: BucketNormal, Start: r.First, End: nr.First,
				Offset: r.Offset1, WallSpan: time.Hour,
				RealSpan: nr.First.t.Sub(r.First.t),
			})
		case WallGap:
			rows = append(rows, HourBucket{Wall: cur, Kind: BucketGap, WallSpan: time.Hour})
		case WallOverlap:
			// cur exists twice. The first bucket covers the summer-offset
			// occurrence; the second covers the winter-offset occurrence.
			rows = append(rows, HourBucket{
				Wall: cur, Kind: BucketOverlapFirst, Start: r.First, End: r.Last,
				Offset: r.Offset1, WallSpan: time.Hour, RealSpan: r.Last.t.Sub(r.First.t),
			})
			nr, err := s.ResolveWall(nextWall)
			if err != nil {
				return nil, err
			}
			rows = append(rows, HourBucket{
				Wall: cur, Kind: BucketOverlapLast, Start: r.Last, End: nr.First,
				Offset: r.Offset2, WallSpan: time.Hour, RealSpan: nr.First.t.Sub(r.Last.t),
			})
		}
		cur = nextWall
	}
	return rows, nil
}

// DailyBucket is one row of a daily report spine.
type DailyBucket struct {
	Wall   LocalDateTime // midnight label in session zone
	Start  Timestamp
	End    Timestamp
	Kind   BucketKind // BucketGap if local midnight does not exist (rare zones)
	Offset int
}

// DailySpine enumerates local calendar days between two instants.
func (s *Session) DailySpine(start, end Timestamp) ([]DailyBucket, error) {
	if !start.Before(end) {
		return nil, nil
	}
	startDay := truncLocal(s.TimestampToLocal(start), TruncDay)
	// end is exclusive: the last included day is whatever day end-1ns lands on.
	lastWanted := truncLocal(s.TimestampToLocal(TimestampFromTime(end.t.Add(-time.Nanosecond))), TruncDay)
	var rows []DailyBucket
	cur := startDay
	for cur.CompareLocal(lastWanted) <= 0 {
		r, err := s.ResolveWall(cur)
		if err != nil {
			return nil, err
		}
		nextDay := cur
		y, m, d := daysToCivil(civilToDays(cur.Year, int(cur.Month), cur.Day) + 1)
		nextDay.Year, nextDay.Month, nextDay.Day = y, m, d
		nr, err := s.ResolveWall(nextDay)
		if err != nil {
			return nil, err
		}
		b := DailyBucket{Wall: cur, Start: r.First, End: nr.First, Offset: r.Offset1}
		if r.Kind == WallGap {
			b.Kind = BucketGap
			b.Start, b.End = Timestamp{}, Timestamp{}
		}
		rows = append(rows, b)
		cur = nextDay
	}
	return rows, nil
}

// Numeric is the constraint on measure types accepted by the aggregation
// helpers (all built-in numeric kinds of the query engine).
type Numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

// HourlyRow is a spine bucket plus the measures accumulated into it.
type HourlyRow[N Numeric] struct {
	HourBucket
	Count int64
	Sum   N
	Has   bool // false for gap buckets and empty real buckets
}

// AggregateHourly assigns each event to its wall-hour bucket by instant and
// sums the measure. Gap buckets are returned with Has=false and Count=0: the
// row exists so the report keeps every hour, but no number is fabricated.
//
// Assignment is by half-open instant containment [Start, End), not by
// truncation: on a fall-back day the same wall label has two distinct buckets
// with disjoint instant intervals, so each occurrence receives its own events
// and the two are never merged.
func (s *Session) AggregateHourly[N Numeric](start, end Timestamp, events []struct {
	TS Timestamp
	V  N
}) ([]HourlyRow[N], error) {
	spine, err := s.HourlySpine(start, end)
	if err != nil {
		return nil, err
	}
	rows := make([]HourlyRow[N], len(spine))
	for i, b := range spine {
		rows[i].HourBucket = b
	}
	for _, e := range events {
		if e.TS.Before(start) || !e.TS.Before(end) {
			continue
		}
		idx := -1
		for i, b := range rows {
			if b.Kind == BucketGap {
				continue
			}
			if (b.Start.Equal(e.TS) || b.Start.Before(e.TS)) && e.TS.Before(b.End) {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		rows[idx].Count++
		rows[idx].Sum += e.V
		rows[idx].Has = true
	}
	return rows, nil
}

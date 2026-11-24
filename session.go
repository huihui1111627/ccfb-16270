package tempora

import (
	"errors"
	"time"
)

// GapPolicy decides what happens when a LocalDateTime falls into a DST gap —
// a wall-clock interval that never exists in the session zone (spring forward).
type GapPolicy int

const (
	// GapForward moves the impossible time forward by exactly the gap length:
	// 02:30 in a 02:00→03:00 spring-forward becomes 03:30. This is the
	// default and matches the common "what clock would actually show" answer.
	GapForward GapPolicy = iota
	// GapBackward moves the impossible time back to just before the jump:
	// 02:30 becomes 01:59:59.999999999 on the old offset.
	GapBackward
	// GapReject makes the conversion an error. Reports that build bucket
	// spines use this to distinguish a real failure from an empty gap bucket.
	GapReject
)

// OverlapPolicy decides what happens when a LocalDateTime falls into a DST
// overlap — a wall-clock interval that occurs twice (fall back).
type OverlapPolicy int

const (
	// OverlapFirst picks the first occurrence, i.e. the earlier instant on
	// the summer (larger) offset. Default.
	OverlapFirst OverlapPolicy = iota
	// OverlapLast picks the second occurrence, i.e. the later instant on the
	// winter (smaller) offset.
	OverlapLast
	// OverlapReject makes the conversion an error.
	OverlapReject
)

// ErrGap is returned under GapReject when a local time does not exist.
var ErrGap = errors.New("tempora: local time does not exist in zone (DST gap)")

// ErrOverlap is returned under OverlapReject when a local time is ambiguous.
var ErrOverlap = errors.New("tempora: local time is ambiguous in zone (DST overlap)")

// Session carries the per-query settings that every zone-dependent operation
// takes as an explicit argument. There is no global mutable zone: two sessions
// with different zones can coexist in the same process, mirroring two
// concurrent connections with different SET TIME ZONE settings.
type Session struct {
	loc     *time.Location
	gap     GapPolicy
	overlap OverlapPolicy
}

// NewSession builds a session for an IANA zone (e.g. "America/New_York").
// The rules come from system/embedded tzdata via time.LoadLocation.
func NewSession(zone string) (*Session, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	return &Session{loc: loc, gap: GapForward, overlap: OverlapFirst}, nil
}

// SessionFromLocation wraps a *time.Location with default policies.
func SessionFromLocation(loc *time.Location) *Session {
	if loc == nil {
		loc = time.UTC
	}
	return &Session{loc: loc, gap: GapForward, overlap: OverlapFirst}
}

// DefaultSession returns a session with the default gap/overlap policies.
func DefaultSession(loc *time.Location) *Session {
	return SessionFromLocation(loc)
}

// WithGapPolicy returns a copy with the given gap policy.
func (s *Session) WithGapPolicy(p GapPolicy) *Session {
	c := *s
	c.gap = p
	return &c
}

// WithOverlapPolicy returns a copy with the given overlap policy.
func (s *Session) WithOverlapPolicy(p OverlapPolicy) *Session {
	c := *s
	c.overlap = p
	return &c
}

// Location returns the session's zone location.
func (s *Session) Location() *time.Location { return s.loc }

// ZoneName returns the IANA name (or "UTC").
func (s *Session) ZoneName() string { return s.loc.String() }

// GapPolicy / OverlapPolicy accessors.
func (s *Session) GapPolicy() GapPolicy         { return s.gap }
func (s *Session) OverlapPolicy() OverlapPolicy { return s.overlap }

// WallKind classifies a wall-clock time in a zone.
type WallKind int

const (
	WallNormal  WallKind = iota // exists exactly once
	WallGap                     // never exists (spring forward)
	WallOverlap                 // exists twice (fall back)
)

func (k WallKind) String() string {
	switch k {
	case WallGap:
		return "gap"
	case WallOverlap:
		return "overlap"
	default:
		return "normal"
	}
}

// WallResolution is the full result of resolving a wall time against tzdata.
type WallResolution struct {
	Kind    WallKind
	First   Timestamp // first/only instant mapping to this wall time
	Last    Timestamp // second instant, set only for overlaps
	Offset1 int       // offset (seconds east of UTC) used by First
	Offset2 int       // offset used by Last; equal to Offset1 for normal
}

// ResolveWall determines exactly how ldt maps to the timeline in the session
// zone, without applying any policy. Detection is self-contained: candidate
// instants are constructed by combining the wall fields with every distinct
// offset observed around the time, then each candidate is projected back to
// wall fields and accepted only if it reproduces the input exactly.
//
// One back-projection  => normal time.
// Two back-projections => overlap (fall back).
// None                 => gap (spring forward); the bracketing transition
//
//	instants are returned in First (last instant on the
//	old offset) and Last (first instant on the new one).
func (s *Session) ResolveWall(ldt LocalDateTime) (WallResolution, error) {
	if !ldt.Valid() {
		return WallResolution{}, ErrInvalidValue
	}
	baseUTC := time.Date(ldt.Year, ldt.Month, ldt.Day, ldt.Hour, ldt.Minute, ldt.Second, ldt.Nsec, time.UTC)

	// Seed with the offset in effect at a provisional instant, then expand:
	// build a provisional instant for every seen offset and read the offsets
	// in effect ±27h around it. Repeating to a fixed point discovers both
	// sides of any transition up to and including a 24h date-line shift.
	offsetSet := map[int]struct{}{}
	_, seed := baseUTC.In(s.loc).Zone()
	offsetSet[seed] = struct{}{}
	for round := 0; round < 3; round++ {
		grew := false
		for off := range offsetSet {
			prov := baseUTC.Add(-time.Duration(off) * time.Second)
			for _, p := range []time.Duration{-27 * time.Hour, -3 * time.Hour, -time.Hour, time.Hour, 3 * time.Hour, 27 * time.Hour} {
				if _, o := prov.Add(p).In(s.loc).Zone(); o != off {
					if _, ok := offsetSet[o]; !ok {
						offsetSet[o] = struct{}{}
						grew = true
					}
				}
			}
		}
		if !grew {
			break
		}
	}

	type cand struct {
		t      time.Time
		offset int
	}
	var matches []cand
	for off := range offsetSet {
		// Candidate instant: wall time interpreted on this offset.
		t := baseUTC.Add(-time.Duration(off) * time.Second)
		// Project back to the zone wall and require an exact field match.
		w := t.In(s.loc)
		if w.Year() == ldt.Year && w.Month() == ldt.Month && w.Day() == ldt.Day &&
			w.Hour() == ldt.Hour && w.Minute() == ldt.Minute &&
			w.Second() == ldt.Second && w.Nanosecond() == ldt.Nsec {
			_, usedOff := w.Zone()
			matches = append(matches, cand{t: t.UTC(), offset: usedOff})
		}
	}
	// Dedup by instant (different probed offsets can yield the same candidate).
	seen := map[int64]struct{}{}
	uniq := matches[:0]
	for _, m := range matches {
		key := m.t.UnixNano()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, m)
	}
	matches = uniq

	switch len(matches) {
	case 1:
		return WallResolution{
			Kind: WallNormal, First: TimestampFromTime(matches[0].t),
			Last:    TimestampFromTime(matches[0].t),
			Offset1: matches[0].offset, Offset2: matches[0].offset,
		}, nil
	case 2:
		if matches[0].t.After(matches[1].t) {
			matches[0], matches[1] = matches[1], matches[0]
		}
		return WallResolution{
			Kind:  WallOverlap,
			First: TimestampFromTime(matches[0].t), Last: TimestampFromTime(matches[1].t),
			Offset1: matches[0].offset, Offset2: matches[1].offset,
		}, nil
	default:
		// Gap. Find the exact transition instant by binary search between
		// two provisional instants known to sit on the old and new offsets.
		minOff, maxOff := 0, 0
		first := true
		for o := range offsetSet {
			if first || o < minOff {
				minOff = o
			}
			if first || o > maxOff {
				maxOff = o
			}
			first = false
		}
		oldOff, newOff := minOff, maxOff
		lo := baseUTC.Add(-time.Duration(oldOff) * time.Second) // old-offset side
		hi := baseUTC.Add(-time.Duration(newOff) * time.Second) // new-offset side
		if lo.After(hi) {
			lo, hi = hi, lo
		}
		boundary := findTransitionInstant(s.loc, lo, hi, oldOff, newOff)
		next := boundary.Add(time.Duration(newOff-oldOff) * time.Second)
		return WallResolution{
			Kind:  WallGap,
			First: TimestampFromTime(boundary), Last: TimestampFromTime(next),
			Offset1: oldOff, Offset2: newOff,
		}, nil
	}
}

// findTransitionInstant returns the first instant in [lo, hi] at which the
// zone offset differs from oldOff, assuming exactly one old->new transition
// inside the bracket. Search is second-accurate; tzdata transitions are
// defined at whole seconds.
func findTransitionInstant(loc *time.Location, lo, hi time.Time, oldOff, newOff int) time.Time {
	lo, hi = lo.UTC(), hi.UTC()
	for hi.Sub(lo) > time.Second {
		mid := lo.Add((hi.Sub(lo)) / 2)
		if _, off := mid.In(loc).Zone(); off == oldOff {
			lo = mid
		} else {
			hi = mid
		}
	}
	_, loOff := lo.In(loc).Zone()
	if loOff == oldOff {
		return hi
	}
	return lo
}

// WallWindow is a half-open interval of local wall times skipped or repeated
// around a transition.
type WallWindow struct {
	From LocalDateTime
	To   LocalDateTime
}

// GapWindow returns the skipped wall-clock interval when r is a gap result,
// or nil otherwise. The boundary instant renders differently on the two
// offsets, so each end is rendered against its explicit offset: New York's
// 2024 spring forward gives skipped wall labels 02:00 -> 03:00.
func (r WallResolution) GapWindow() *WallWindow {
	if r.Kind != WallGap {
		return nil
	}
	from := renderAtOffset(r.First.t, r.Offset1)
	to := renderAtOffset(r.First.t, r.Offset2)
	return &WallWindow{from, to}
}

// OverlapWindow returns the repeated wall-clock interval when r is an overlap
// result, or nil otherwise. New York's 2024 fall back yields 01:00 -> 02:00.
func (r WallResolution) OverlapWindow() *WallWindow {
	if r.Kind != WallOverlap {
		return nil
	}
	from := renderAtOffset(r.First.t, r.Offset1)
	// The window's wall length equals the offset delta; the end label is the
	// start label advanced by that wall duration.
	wallLen := int64(r.Offset1 - r.Offset2)
	to := addWallNanoseconds(from, wallLen*int64(Second))
	return &WallWindow{from, to}
}

// renderAtOffset renders an instant as wall fields under an explicit offset
// rather than the offset currently in effect at that instant — required when
// reconstructing the two sides of a transition boundary.
func renderAtOffset(t time.Time, offsetSec int) LocalDateTime {
	w := t.UTC().Add(time.Duration(offsetSec) * time.Second)
	return LocalDateTime{
		Year: w.Year(), Month: w.Month(), Day: w.Day(),
		Hour: w.Hour(), Minute: w.Minute(), Second: w.Second(), Nsec: w.Nanosecond(),
	}
}

func wallOf(t time.Time) LocalDateTime {
	w := t.UTC()
	return LocalDateTime{
		Year: w.Year(), Month: w.Month(), Day: w.Day(),
		Hour: w.Hour(), Minute: w.Minute(), Second: w.Second(), Nsec: w.Nanosecond(),
	}
}

// LocalToTimestamp converts a wall time to an instant, applying the session's
// gap/overlap policies.
func (s *Session) LocalToTimestamp(ldt LocalDateTime) (Timestamp, error) {
	r, err := s.ResolveWall(ldt)
	if err != nil {
		return Timestamp{}, err
	}
	switch r.Kind {
	case WallNormal, WallOverlap:
		if r.Kind == WallOverlap {
			switch s.overlap {
			case OverlapFirst:
				return r.First, nil
			case OverlapLast:
				return r.Last, nil
			case OverlapReject:
				return Timestamp{}, ErrOverlap
			}
		}
		return r.First, nil
	default: // gap
		w := r.GapWindow()
		switch s.gap {
		case GapForward:
			// Move the wall clock forward across the whole gap: the input's
			// position inside the skipped interval is preserved on the
			// post-jump timeline. 02:30 in a 02:00->03:00 jump => 03:30.
			delta := time.Duration(LocalDiff(ldt, w.From).Nanoseconds)
			return TimestampFromTime(r.First.t.Add(delta)), nil
		case GapBackward:
			// Move the wall clock backward across the whole gap: 02:30 maps
			// to the equivalent position on the pre-jump timeline, 01:30.
			moved := addWallNanoseconds(ldt, -int64(time.Duration(r.Offset2-r.Offset1)*time.Second))
			out, err := s.LocalToTimestamp(moved)
			if err != nil {
				return Timestamp{}, err
			}
			return out, nil
		case GapReject:
			return Timestamp{}, ErrGap
		default:
			return Timestamp{}, ErrGap
		}
	}
}

// TimestampToLocal renders an absolute instant as wall fields in the session
// zone. This conversion is always total: every instant has one wall time.
func (s *Session) TimestampToLocal(ts Timestamp) LocalDateTime {
	w := ts.t.In(s.loc)
	return LocalDateTime{
		Year: w.Year(), Month: w.Month(), Day: w.Day(),
		Hour: w.Hour(), Minute: w.Minute(), Second: w.Second(), Nsec: w.Nanosecond(),
	}
}

// TimestampToDate renders the calendar date of an instant in the session zone.
func (s *Session) TimestampToDate(ts Timestamp) Date {
	w := ts.t.In(s.loc)
	return Date{w.Year(), w.Month(), w.Day()}
}

// LocalDateAtMidnight converts a pure date to the instant of local midnight,
// applying the gap policy (midnight itself can be missing in rare zones).
func (s *Session) LocalDateAtMidnight(d Date) (Timestamp, error) {
	return s.LocalToTimestamp(d.AtMidnight())
}

// OffsetSeconds returns the zone offset in effect at the given instant.
func (s *Session) OffsetSeconds(ts Timestamp) int {
	_, off := ts.t.In(s.loc).Zone()
	return off
}

// ZoneAbbr returns the zone abbreviation in effect at the given instant.
func (s *Session) ZoneAbbr(ts Timestamp) string {
	name, _ := ts.t.In(s.loc).Zone()
	return name
}

package tempora

import (
	"math/rand"
	"testing"
	"time"
)

// referenceResolve is an INDEPENDENT re-implementation of wall-time resolution
// using only stdlib primitives (time.Date / FixedZone / Zone). It mirrors the
// engine's candidate-and-project method but shares no code with session.go,
// so a shared bug in the engine logic cannot silently validate itself.
func referenceResolve(t *testing.T, loc *time.Location, ldt LocalDateTime) WallResolution {
	t.Helper()
	base := time.Date(ldt.Year, ldt.Month, ldt.Day, ldt.Hour, ldt.Minute, ldt.Second, ldt.Nsec, time.UTC)
	_, seed := base.In(loc).Zone()
	offs := map[int]struct{}{seed: {}}
	for round := 0; round < 3; round++ {
		grew := false
		for o := range offs {
			prov := base.Add(-time.Duration(o) * time.Second)
			for _, p := range []time.Duration{-27 * time.Hour, -3 * time.Hour, -time.Hour, time.Hour, 3 * time.Hour, 27 * time.Hour} {
				if _, no := prov.Add(p).In(loc).Zone(); no != o {
					if _, ok := offs[no]; !ok {
						offs[no] = struct{}{}
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
		t   time.Time
		off int
	}
	var ms []cand
	for o := range offs {
		candT := base.Add(-time.Duration(o) * time.Second)
		w := candT.In(loc)
		if w.Year() == ldt.Year && w.Month() == ldt.Month && w.Day() == ldt.Day &&
			w.Hour() == ldt.Hour && w.Minute() == ldt.Minute && w.Second() == ldt.Second &&
			w.Nanosecond() == ldt.Nsec {
			_, used := w.Zone()
			ms = append(ms, cand{candT.UTC(), used})
		}
	}
	dedup := map[int64]cand{}
	for _, c := range ms {
		dedup[c.t.UnixNano()] = c
	}
	ms = ms[:0]
	for _, c := range dedup {
		ms = append(ms, c)
	}
	switch len(ms) {
	case 1:
		return WallResolution{Kind: WallNormal, First: TimestampFromTime(ms[0].t),
			Last: TimestampFromTime(ms[0].t), Offset1: ms[0].off, Offset2: ms[0].off}
	case 2:
		if ms[0].t.After(ms[1].t) {
			ms[0], ms[1] = ms[1], ms[0]
		}
		return WallResolution{Kind: WallOverlap, First: TimestampFromTime(ms[0].t),
			Last: TimestampFromTime(ms[1].t), Offset1: ms[0].off, Offset2: ms[1].off}
	default:
		minOff, maxOff := 0, 0
		first := true
		for o := range offs {
			if first || o < minOff {
				minOff = o
			}
			if first || o > maxOff {
				maxOff = o
			}
			first = false
		}
		oldOff, newOff := minOff, maxOff
		lo := base.Add(-time.Duration(oldOff) * time.Second)
		hi := base.Add(-time.Duration(newOff) * time.Second)
		if lo.After(hi) {
			lo, hi = hi, lo
		}
		lo, hi = lo.UTC(), hi.UTC()
		for hi.Sub(lo) > time.Second {
			mid := lo.Add((hi.Sub(lo)) / 2)
			if _, o := mid.In(loc).Zone(); o == oldOff {
				lo = mid
			} else {
				hi = mid
			}
		}
		boundary := hi
		if _, o := lo.In(loc).Zone(); o != oldOff {
			boundary = lo
		}
		jumpLen := time.Duration(newOff-oldOff) * time.Second
		return WallResolution{Kind: WallGap, First: TimestampFromTime(boundary),
			Last: TimestampFromTime(boundary.Add(jumpLen)), Offset1: oldOff, Offset2: newOff}
	}
}

func ldt(y int, m time.Month, d, h, mi int) LocalDateTime {
	return LocalDateTime{y, m, d, h, mi, 0, 0}
}

func TestNYCSpringForward2024(t *testing.T) {
	sess, err := NewSession("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 02:30 on 2024-03-10 does not exist.
	r, err := sess.ResolveWall(ldt(2024, time.March, 10, 2, 30))
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != WallGap {
		t.Fatalf("kind = %s, want gap", r.Kind)
	}
	if r.Offset1 != -5*3600 || r.Offset2 != -4*3600 {
		t.Fatalf("offsets = %d/%d, want -18000/-14400", r.Offset1, r.Offset2)
	}
	// Transition boundary: 02:00 EST = 07:00 UTC; next valid instant 03:00 EDT.
	wantFirst := time.Date(2024, time.March, 10, 7, 0, 0, 0, time.UTC)
	wantLast := time.Date(2024, time.March, 10, 8, 0, 0, 0, time.UTC)
	if !r.First.Time().Equal(wantFirst) || !r.Last.Time().Equal(wantLast) {
		t.Fatalf("gap bracket = %v..%v, want %v..%v", r.First.Time(), r.Last.Time(), wantFirst, wantLast)
	}
	// Gap window wall fields: the skipped wall times 02:00 -> 03:00.
	w := r.GapWindow()
	if w == nil || w.From != ldt(2024, time.March, 10, 2, 0) || w.To != ldt(2024, time.March, 10, 3, 0) {
		t.Fatalf("gap wall window = %+v, want 02:00..03:00", w)
	}
	// Policies.
	gap := ldt(2024, time.March, 10, 2, 30)
	fwd, err := sess.WithGapPolicy(GapForward).LocalToTimestamp(gap)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2024, time.March, 10, 3, 30, 0, 0, sess.Location()); !fwd.Time().Equal(want.UTC()) {
		t.Fatalf("forward = %v, want %v", fwd.Time(), want)
	}
	back, err := sess.WithGapPolicy(GapBackward).LocalToTimestamp(gap)
	if err != nil {
		t.Fatal(err)
	}
	// Backward shifts the whole wall interval: 02:30 -> 01:30 EST (06:30 UTC).
	wantBack := time.Date(2024, time.March, 10, 6, 30, 0, 0, time.UTC)
	if !back.Time().Equal(wantBack) {
		t.Fatalf("backward = %v, want %v", back.Time(), wantBack)
	}
	if _, err := sess.WithGapPolicy(GapReject).LocalToTimestamp(gap); err != ErrGap {
		t.Fatalf("reject policy err = %v, want ErrGap", err)
	}
}

func TestNYCFallBack2024(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	// 01:30 on 2024-11-03 occurs twice: EDT (-04) then EST (-05).
	r, err := sess.ResolveWall(ldt(2024, time.November, 3, 1, 30))
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != WallOverlap {
		t.Fatalf("kind = %s, want overlap", r.Kind)
	}
	wantFirst := time.Date(2024, time.November, 3, 5, 30, 0, 0, time.UTC) // 01:30 -04:00
	wantLast := time.Date(2024, time.November, 3, 6, 30, 0, 0, time.UTC)  // 01:30 -05:00
	if !r.First.Time().Equal(wantFirst) || !r.Last.Time().Equal(wantLast) {
		t.Fatalf("overlap = %v..%v, want %v..%v", r.First.Time(), r.Last.Time(), wantFirst, wantLast)
	}
	if r.Last.Time().Sub(r.First.Time()) != time.Hour {
		t.Fatalf("occurrences not 1h apart: %v", r.Last.Time().Sub(r.First.Time()))
	}
	wall := ldt(2024, time.November, 3, 1, 30)
	first, _ := sess.WithOverlapPolicy(OverlapFirst).LocalToTimestamp(wall)
	last, _ := sess.WithOverlapPolicy(OverlapLast).LocalToTimestamp(wall)
	if !first.Time().Equal(wantFirst) || !last.Time().Equal(wantLast) {
		t.Fatalf("policies gave %v / %v", first.Time(), last.Time())
	}
	if _, err := sess.WithOverlapPolicy(OverlapReject).LocalToTimestamp(wall); err != ErrOverlap {
		t.Fatalf("reject policy err = %v, want ErrOverlap", err)
	}
}

// TestGoldenGrid sweeps transition days and random days in many zones and
// compares every 30-minute wall sample against the independent reference.
func TestGoldenGrid(t *testing.T) {
	zones := []string{
		"America/New_York", "Europe/London", "Europe/Berlin", "Australia/Sydney",
		"Asia/Kolkata", "Pacific/Apia", "America/Sao_Paulo",
		"Australia/Lord_Howe", "Asia/Shanghai", "Asia/Tehran", "America/Havana",
	}
	// Transition-adjacent days plus a deterministic pseudo-random spread.
	days := make([]Date, 0, 64)
	for _, ymd := range [][3]int{
		{2024, 3, 10}, {2024, 3, 11}, {2024, 11, 3}, {2024, 11, 4},
		{2024, 3, 31}, {2024, 10, 27}, {2024, 4, 7}, {2024, 10, 6},
		{2011, 12, 30}, {2011, 12, 31}, {2019, 10, 20}, {2014, 10, 26},
		{1949, 5, 27}, {1949, 5, 28}, {1943, 9, 12},
	} {
		d, _ := NewDate(ymd[0], time.Month(ymd[1]), ymd[2])
		days = append(days, d)
	}
	rng := rand.New(rand.NewSource(42))
	for k := 0; k < 30; k++ {
		y := 1918 + rng.Intn(109)
		m := time.Month(1 + rng.Intn(12))
		d, _ := NewDate(y, m, 1+rng.Intn(daysInMonth(y, m)))
		days = append(days, d)
	}
	for _, z := range zones {
		loc, err := time.LoadLocation(z)
		if err != nil {
			t.Fatalf("zone %s: %v", z, err)
		}
		sess := SessionFromLocation(loc)
		for _, day := range days {
			for h := 0; h < 24; h++ {
				for _, mi := range []int{0, 30} {
					l := LocalDateTime{day.Year, day.Month, day.Day, h, mi, rng.Intn(2), rng.Intn(1_000_000)}
					got, gerr := sess.ResolveWall(l)
					want := referenceResolve(t, loc, l)
					if gerr != nil {
						t.Fatalf("%s %v: engine error %v", z, l, gerr)
					}
					if got.Kind != want.Kind {
						t.Fatalf("%s %v: kind %s, reference %s", z, l, got.Kind, want.Kind)
					}
					if !got.First.Equal(want.First) || !got.Last.Equal(want.Last) {
						t.Fatalf("%s %v: instants %v..%v, reference %v..%v",
							z, l, got.First.Time(), got.Last.Time(), want.First.Time(), want.Last.Time())
					}
					if got.Offset1 != want.Offset1 || got.Offset2 != want.Offset2 {
						t.Fatalf("%s %v: offsets %d/%d, reference %d/%d",
							z, l, got.Offset1, got.Offset2, want.Offset1, want.Offset2)
					}
				}
			}
		}
	}
}

// TestLordHoweHalfHour covers a 30-minute DST transition.
func TestLordHoweHalfHour(t *testing.T) {
	sess, _ := NewSession("Australia/Lord_Howe")
	// First Sunday of October 2024: 2024-10-06, clocks 02:00 +10:30 -> 02:30 +11:00.
	r, err := sess.ResolveWall(ldt(2024, time.October, 6, 2, 15))
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != WallGap {
		t.Fatalf("kind = %s, want gap", r.Kind)
	}
	if r.Offset1 != 37800 || r.Offset2 != 39600 { // +10:30, +11:00
		t.Fatalf("half-hour offsets = %d/%d", r.Offset1, r.Offset2)
	}
}

// TestApiaMissingDay covers the 2011 24-hour date-line jump: Dec 30 never existed.
func TestApiaMissingDay(t *testing.T) {
	sess, _ := NewSession("Pacific/Apia")
	r, err := sess.ResolveWall(ldt(2011, time.December, 30, 12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != WallGap {
		t.Fatalf("kind = %s, want gap", r.Kind)
	}
	if r.Last.Time().Sub(r.First.Time()) != 24*time.Hour {
		t.Fatalf("Apia gap length = %v, want 24h", r.Last.Time().Sub(r.First.Time()))
	}
}

func TestInstantEqualityAcrossZones(t *testing.T) {
	ny, _ := NewSession("America/New_York")
	sh, _ := NewSession("Asia/Shanghai")
	// 2024-03-15 08:00 EDT == 2024-03-15 20:00 CST (same instant).
	a, err := ny.LocalToTimestamp(ldt(2024, time.March, 15, 8, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := sh.LocalToTimestamp(ldt(2024, time.March, 15, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) || a.Compare(b) != 0 || a.JoinKey() != b.JoinKey() {
		t.Fatalf("same instant in different zones not equal: %v vs %v", a.Time(), b.Time())
	}
	if a.Unix() != b.Unix() {
		t.Fatalf("epoch seconds differ")
	}
}

func TestArithmeticAcrossDST(t *testing.T) {
	sess, _ := NewSession("America/New_York")
	// Adding one calendar day to an instant spans 23h on spring day, 25h fall.
	ts, _ := sess.LocalToTimestamp(ldt(2024, time.March, 9, 12, 0))
	next, err := sess.Add(ts, DaysInterval(1))
	if err != nil {
		t.Fatal(err)
	}
	if d := next.Time().Sub(ts.Time()); d != 23*time.Hour {
		t.Fatalf("spring day length = %v, want 23h", d)
	}
	if l := sess.TimestampToLocal(next); l != ldt(2024, time.March, 10, 12, 0) {
		t.Fatalf("wall after +1 day = %v, want 12:00 next day", l)
	}
	ts2, _ := sess.LocalToTimestamp(ldt(2024, time.November, 2, 12, 0))
	next2, _ := sess.Add(ts2, DaysInterval(1))
	if d := next2.Time().Sub(ts2.Time()); d != 25*time.Hour {
		t.Fatalf("fall day length = %v, want 25h", d)
	}
	// Exact-time arithmetic is DST-proof.
	plus24, _ := sess.Add(ts, HoursInterval(24))
	if d := plus24.Time().Sub(ts.Time()); d != 24*time.Hour {
		t.Fatalf("exact 24h = %v", d)
	}
	wall := sess.TimestampToLocal(plus24)
	if wall != ldt(2024, time.March, 10, 13, 0) { // 12:00 + 24 real hours lands 13:00 wall
		t.Fatalf("exact-24h wall = %v, want 13:00", wall)
	}
}

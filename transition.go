package tempora

import "time"

// Transition describes one offset change inside an observed window.
type Transition struct {
	AtUTC         Timestamp
	OldOffset     int
	NewOffset     int
	OldAbbr       string
	NewAbbr       string
	Delta         time.Duration // NewOffset - OldOffset (positive => spring forward gap)
	GapWindow     *WallWindow   // set when local wall times are skipped
	OverlapWindow *WallWindow   // set when local wall times repeat
}

// TransitionsOn returns every offset transition affecting the given local
// calendar date in the session zone (normally zero or one). It scans the
// surrounding 48 hours in UTC at one-minute resolution to locate the changed
// minute, then re-scans that minute per second for an exact boundary. This
// deliberately does not rely on undocumented time-package internals.
func (s *Session) TransitionsOn(d Date) ([]Transition, error) {
	start, err := s.LocalDateAtMidnight(d)
	if err != nil {
		return nil, err
	}
	windowStart := start.t.Add(-2 * time.Hour)
	windowEnd := start.t.Add(26 * time.Hour)

	var changes []struct {
		at             time.Time
		oldOff, newOff int
	}
	prev := windowStart
	_, prevOff := prev.In(s.loc).Zone()
	for cur := windowStart.Add(time.Minute); !cur.After(windowEnd); cur = cur.Add(time.Minute) {
		_, off := cur.In(s.loc).Zone()
		if off != prevOff {
			// Refine within [prev, cur] to the exact second.
			exact := refineTransition(s.loc, prev, cur, prevOff, off)
			changes = append(changes, struct {
				at             time.Time
				oldOff, newOff int
			}{exact, prevOff, off})
			prevOff = off
			prev = cur
			continue
		}
		prev = cur
	}

	out := make([]Transition, 0, len(changes))
	for _, ch := range changes {
		at := TimestampFromTime(ch.at)
		tr := Transition{
			AtUTC:     at,
			OldOffset: ch.oldOff,
			NewOffset: ch.newOff,
			Delta:     time.Duration(ch.newOff-ch.oldOff) * time.Second,
		}
		tr.OldAbbr = ch.at.Add(-time.Second).In(s.loc).Format("MST")
		tr.NewAbbr = ch.at.In(s.loc).Format("MST")
		delta := ch.newOff - ch.oldOff
		// Pick a wall label guaranteed to sit INSIDE the affected interval:
		//   gap (delta>0): render on the OLD offset -> first skipped label
		//     07:00 UTC at -05:00 = 02:00, the start of the 02:00->03:00 gap
		//   overlap (delta<0): render on the NEW offset -> first repeated label
		//     06:00 UTC at -05:00 = 01:00, inside the repeated 01:00->02:00
		var jumpWall LocalDateTime
		if delta > 0 {
			jumpWall = wallOf(ch.at.Add(time.Duration(ch.oldOff) * time.Second))
		} else {
			jumpWall = wallOf(ch.at.Add(time.Duration(ch.newOff) * time.Second))
		}
		res, err := s.ResolveWall(jumpWall)
		if err == nil {
			if delta > 0 {
				tr.GapWindow = res.GapWindow()
			} else if delta < 0 {
				tr.OverlapWindow = res.OverlapWindow()
			}
		}
		out = append(out, tr)
	}
	return out, nil
}

// refineTransition narrows a known offset change to the exact second.
func refineTransition(loc *time.Location, lo, hi time.Time, oldOff, newOff int) time.Time {
	for hi.Sub(lo) > time.Second {
		mid := lo.Add((hi.Sub(lo)) / 2)
		if _, off := mid.In(loc).Zone(); off == oldOff {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// LocalTimeline renders the 24 wall-hour labels of a date with per-hour
// classification, suitable for the UI's "how the local axis jumps" view.
type TimelineHour struct {
	Wall   LocalDateTime
	Kind   WallKind
	Start  Timestamp // zero for gap
	End    Timestamp
	Offset int
}

// LocalTimeline returns the hour-by-hour shape of one local date, including
// gap markers and both overlap occurrences. It enumerates the 24 wall labels
// directly rather than walking instants, so it stays correct even when local
// midnight itself falls inside a gap (e.g. Pacific/Apia's missing 2011-12-30).
func (s *Session) LocalTimeline(d Date) ([]TimelineHour, error) {
	out := make([]TimelineHour, 0, 24)
	for h := 0; h < 24; h++ {
		wall := LocalDateTime{Year: d.Year, Month: d.Month, Day: d.Day, Hour: h}
		r, err := s.ResolveWall(wall)
		if err != nil {
			return nil, err
		}
		switch r.Kind {
		case WallGap:
			out = append(out, TimelineHour{Wall: wall, Kind: WallGap,
				Start: Timestamp{}, End: Timestamp{}, Offset: r.Offset1})
		case WallOverlap:
			out = append(out, TimelineHour{Wall: wall, Kind: WallOverlap,
				Start: r.First, End: r.Last, Offset: r.Offset1})
		default:
			out = append(out, TimelineHour{Wall: wall, Kind: WallNormal,
				Start: r.First, End: r.Last, Offset: r.Offset1})
		}
	}
	return out, nil
}

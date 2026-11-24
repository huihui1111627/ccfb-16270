package tempora

import (
	"strconv"
	"strings"
	"time"
)

// This file implements formatting independently of time.Time's layout
// mechanism. A strftime-inspired token set is supported, plus one-call ISO
// helpers for the common cases.
//
// Tokens:
//
//	%Y  4-digit year          %m  2-digit month (01-12)
//	%d  2-digit day (01-31)   %H  2-digit hour 00-23
//	%M  2-digit minute        %S  2-digit second
//	%f  9-digit fractional seconds (no decimal point)
//	%z  offset +hhmm          %:z offset +hh:mm
//	%Z  zone abbreviation     %b  abbreviated English month name
//	%B  full English month    %a  abbreviated weekday   %A full weekday
//	%%  literal %
//
// Tokens needing zone information (%z %:z %Z) are only valid for Timestamp
// formatting through a Session.

func pad2(b *strings.Builder, v int) {
	b.WriteByte(byte('0' + v/10))
	b.WriteByte(byte('0' + v%10))
}

func pad4(b *strings.Builder, v int) {
	s := strconv.Itoa(v)
	for k := len(s); k < 4; k++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

// FormatLocal renders a zone-less wall time. Zone tokens are rejected.
func FormatLocal(l LocalDateTime, layout string) (string, error) {
	var b strings.Builder
	err := renderLayout(&b, layout, l, nil, 0, "")
	return b.String(), err
}

// FormatDate renders a pure date (only date tokens are meaningful).
func FormatDate(d Date, layout string) (string, error) {
	return FormatLocal(d.AtMidnight(), layout)
}

// FormatTimestamp renders an instant as observed in the session zone.
func (s *Session) FormatTimestamp(ts Timestamp, layout string) (string, error) {
	l := s.TimestampToLocal(ts)
	off := s.OffsetSeconds(ts)
	abbr := s.ZoneAbbr(ts)
	var b strings.Builder
	err := renderLayout(&b, layout, l, &ts, off, abbr)
	return b.String(), err
}

// ISO helpers --------------------------------------------------------------

// ISOLocal is the canonical zone-less form: 2024-03-10T02:30:00[.fraction].
func ISOLocal(l LocalDateTime) string {
	var b strings.Builder
	writeDate(&b, l.Year, int(l.Month), l.Day)
	b.WriteByte('T')
	writeTime(&b, l)
	return b.String()
}

// ISODate is the canonical date form: 2024-03-10.
func ISODate(d Date) string {
	var b strings.Builder
	writeDate(&b, d.Year, int(d.Month), d.Day)
	return b.String()
}

// ISOTimestamp renders an instant with an explicit offset in the session zone,
// e.g. 2024-03-10T03:00:00-04:00.
func (s *Session) ISOTimestamp(ts Timestamp) string {
	l := s.TimestampToLocal(ts)
	var b strings.Builder
	writeDate(&b, l.Year, int(l.Month), l.Day)
	b.WriteByte('T')
	writeTime(&b, l)
	writeOffsetColon(&b, s.OffsetSeconds(ts))
	return b.String()
}

func writeDate(b *strings.Builder, y, m, d int) {
	pad4(b, y)
	b.WriteByte('-')
	pad2(b, m)
	b.WriteByte('-')
	pad2(b, d)
}

func writeTime(b *strings.Builder, l LocalDateTime) {
	pad2(b, l.Hour)
	b.WriteByte(':')
	pad2(b, l.Minute)
	b.WriteByte(':')
	pad2(b, l.Second)
	if l.Nsec != 0 {
		b.WriteByte('.')
		s := strconv.Itoa(l.Nsec)
		for k := len(s); k < 9; k++ {
			b.WriteByte('0')
		}
		b.WriteString(s)
	}
}

func writeOffsetColon(b *strings.Builder, off int) {
	if off == 0 {
		b.WriteByte('Z')
		return
	}
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	b.WriteRune(sign)
	pad2(b, off/3600)
	b.WriteByte(':')
	pad2(b, (off%3600)/60)
}

var monthAbbr = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
var monthFull = []string{"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December"}
var dayAbbr = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
var dayFull = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func renderLayout(b *strings.Builder, layout string, l LocalDateTime,
	ts *Timestamp, off int, abbr string) error {
	for i := 0; i < len(layout); i++ {
		c := layout[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(layout) {
			return parseErr("dangling '%' in layout")
		}
		tok := layout[i]
		if tok == ':' && i+1 < len(layout) && layout[i+1] == 'z' {
			tok = 'Z' + 1 // sentinel for %:z
			i++
		}
		switch tok {
		case 'Y':
			pad4(b, l.Year)
		case 'm':
			pad2(b, int(l.Month))
		case 'd':
			pad2(b, l.Day)
		case 'H':
			pad2(b, l.Hour)
		case 'M':
			pad2(b, l.Minute)
		case 'S':
			pad2(b, l.Second)
		case 'f':
			s := strconv.Itoa(l.Nsec)
			for k := len(s); k < 9; k++ {
				b.WriteByte('0')
			}
			b.WriteString(s)
		case 'b':
			b.WriteString(monthAbbr[l.Month-1])
		case 'B':
			b.WriteString(monthFull[l.Month-1])
		case 'a', 'A':
			wd := weekday(civilToDays(l.Year, int(l.Month), l.Day))
			if tok == 'a' {
				b.WriteString(dayAbbr[wd])
			} else {
				b.WriteString(dayFull[wd])
			}
		case 'z':
			if ts == nil {
				return parseErr("%%z requires a timestamp with a session")
			}
			writeOffsetBasic(b, off)
		case 'Z' + 1: // %:z
			if ts == nil {
				return parseErr("%%:z requires a timestamp with a session")
			}
			writeOffsetColon(b, off)
		case 'Z':
			if ts == nil {
				return parseErr("%%Z requires a timestamp with a session")
			}
			b.WriteString(abbr)
		case '%':
			b.WriteByte('%')
		default:
			return parseErr("unsupported layout token %" + string(tok))
		}
	}
	return nil
}

func writeOffsetBasic(b *strings.Builder, off int) {
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	b.WriteRune(sign)
	pad2(b, off/3600)
	pad2(b, (off%3600)/60)
}

// ensure time package stays used even if future edits drop other references.
var _ = time.UTC

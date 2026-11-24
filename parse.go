package tempora

import (
	"errors"
	"strings"
	"time"
)

// This file is a hand-written scanner for the common textual forms. Nothing is
// delegated to time.Parse: component validation, fraction handling, offset and
// IANA-zone suffixes, and the error for illegal calendar dates are all owned
// here so the semantics are explicit and stable.
//
// Accepted forms (spaces may replace 'T'):
//
//	2024-03-10                              -> Date
//	2024/03/10                              -> Date
//	2024-03-10T02:30:00                     -> LocalDateTime (no zone)
//	2024-03-10 02:30:00.123456789           -> LocalDateTime
//	2024-03-10T02:30:00Z                    -> Timestamp (UTC)
//	2024-03-10T02:30:00+08:00               -> Timestamp (fixed offset)
//	2024-03-10T02:30:00+0800 / +08          -> Timestamp
//	2024-03-10T02:30:00[America/New_York]   -> Timestamp (IANA, ISO-8601-2)
//	2024-03-10T02:30:00 America/New_York    -> Timestamp (IANA, space form)
//
// Ambiguous abbreviations such as CST are rejected: callers must give an
// explicit offset or an IANA name. Illegal calendar dates (2023-02-29,
// 2024-13-01, hour 24) return ErrInvalidValue rather than being normalized.

// ErrParse is the base error for unparseable time strings.
var ErrParse = errors.New("tempora: failed to parse time string")

func parseErr(msg string) error {
	return errors.New(strings.TrimRight("tempora: failed to parse time string: "+msg, ": "))
}

type scan struct {
	s string
	i int
}

func (sc *scan) eof() bool { return sc.i >= len(sc.s) }

func (sc *scan) peek() byte {
	if sc.eof() {
		return 0
	}
	return sc.s[sc.i]
}

func (sc *scan) digits(n int) (int, bool) {
	if sc.i+n > len(sc.s) {
		return 0, false
	}
	v := 0
	for k := 0; k < n; k++ {
		c := sc.s[sc.i+k]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	sc.i += n
	return v, true
}

func (sc *scan) lit(c byte) bool {
	if !sc.eof() && sc.s[sc.i] == c {
		sc.i++
		return true
	}
	return false
}

// parsedParts holds everything the scanner can extract.
type parsedParts struct {
	year, month, day           int
	hour, minute, second, nsec int
	hasTime                    bool
	hasZone                    bool
	loc                        *time.Location
}

// ParseDate parses a bare calendar date.
func ParseDate(s string) (Date, error) {
	sc := &scan{s: strings.TrimSpace(s)}
	y, m, d, err := scanDate(sc)
	if err != nil {
		return Date{}, err
	}
	if !sc.eof() {
		return Date{}, parseErr("unexpected trailing characters in date")
	}
	return NewDate(y, time.Month(m), d)
}

// ParseLocalDateTime parses a zone-less wall time. Any zone/offset suffix is
// an error: use ParseTimestamp for instants.
func ParseLocalDateTime(s string) (LocalDateTime, error) {
	sc := &scan{s: strings.TrimSpace(s)}
	p, err := scanDateTime(sc)
	if err != nil {
		return LocalDateTime{}, err
	}
	if !sc.eof() {
		return LocalDateTime{}, parseErr("unexpected trailing characters")
	}
	if p.hasZone {
		return LocalDateTime{}, parseErr("local date/time must not carry a time zone")
	}
	if !p.hasTime {
		return LocalDateTime{}, parseErr("time component required")
	}
	return NewLocalDateTime(p.year, time.Month(p.month), p.day, p.hour, p.minute, p.second, p.nsec)
}

// ParseTimestamp parses an instant. Offset and IANA suffixes are resolved via
// tzdata; DST gap/overlap uses the default policy.
func ParseTimestamp(s string) (Timestamp, error) {
	return ParseTimestampSession(s, nil)
}

// ParseTimestampSession parses an instant and applies the session's gap/
// overlap policies when the suffix names an IANA zone. A nil session means
// default policy in the named zone.
func ParseTimestampSession(s string, sess *Session) (Timestamp, error) {
	sc := &scan{s: strings.TrimSpace(s)}
	p, err := scanDateTime(sc)
	if err != nil {
		return Timestamp{}, err
	}
	if !sc.eof() {
		return Timestamp{}, parseErr("unexpected trailing characters")
	}
	if !p.hasZone {
		return Timestamp{}, parseErr("timestamp requires an offset or time zone")
	}
	ldt, err := NewLocalDateTime(p.year, time.Month(p.month), p.day, p.hour, p.minute, p.second, p.nsec)
	if err != nil {
		return Timestamp{}, err
	}
	// Apply the caller's policies when the parsed zone matches the session
	// zone by IANA name (pointer identity is not guaranteed across
	// LoadLocation calls). Fixed-offset zones always use the default policy.
	if sess != nil && p.loc.String() == sess.loc.String() && p.loc.String() != "" {
		return sess.LocalToTimestamp(ldt)
	}
	return DefaultSession(p.loc).LocalToTimestamp(ldt)
}

func scanDate(sc *scan) (y, m, d int, err error) {
	var ok bool
	if y, ok = sc.digits(4); !ok {
		return 0, 0, 0, parseErr("expected 4-digit year")
	}
	sep := byte('-')
	if sc.peek() == '/' {
		sep = '/'
	}
	if !sc.lit(sep) {
		return 0, 0, 0, parseErr("expected '-' or '/' after year")
	}
	if m, ok = sc.digits(2); !ok {
		return 0, 0, 0, parseErr("expected 2-digit month")
	}
	if !sc.lit(sep) {
		return 0, 0, 0, parseErr("expected date separator before day")
	}
	if d, ok = sc.digits(2); !ok {
		return 0, 0, 0, parseErr("expected 2-digit day")
	}
	return y, m, d, nil
}

func scanDateTime(sc *scan) (parsedParts, error) {
	var p parsedParts
	y, m, d, err := scanDate(sc)
	if err != nil {
		return p, err
	}
	p.year, p.month, p.day = y, m, d
	if sc.eof() {
		return p, nil
	}
	// Date-time separator: 'T', 't' or whitespace.
	c := sc.peek()
	if c == 'T' || c == 't' {
		sc.i++
	} else if c == ' ' {
		sc.i++
		for sc.peek() == ' ' {
			sc.i++
		}
	} else {
		return p, parseErr("expected 'T' or space between date and time")
	}
	hour, ok := sc.digits(2)
	if !ok {
		return p, parseErr("expected 2-digit hour")
	}
	p.hour = hour
	p.hasTime = true
	if !sc.lit(':') {
		return p, parseErr("expected ':' after hour")
	}
	if p.minute, ok = sc.digits(2); !ok {
		return p, parseErr("expected 2-digit minute")
	}
	p.second = 0
	if sc.lit(':') {
		if p.second, ok = sc.digits(2); !ok {
			return p, parseErr("expected 2-digit second")
		}
	}
	if sc.peek() == '.' || sc.peek() == ',' {
		sc.i++
		start := sc.i
		nsec := 0
		digits := 0
		for !sc.eof() {
			ch := sc.peek()
			if ch < '0' || ch > '9' {
				break
			}
			if digits < 9 {
				nsec = nsec*10 + int(ch-'0')
			}
			digits++
			sc.i++
		}
		if digits == 0 {
			return p, parseErr("fractional separator without digits")
		}
		if digits > 9 {
			return p, parseErr("sub-nanosecond precision not supported")
		}
		for k := digits; k < 9; k++ {
			nsec *= 10
		}
		p.nsec = nsec
		_ = start
	}
	// Zone suffix.
	loc, err := scanZone(sc)
	if err != nil {
		return p, err
	}
	if loc != nil {
		p.hasZone = true
		p.loc = loc
	}
	return p, nil
}

func scanZone(sc *scan) (*time.Location, error) {
	if sc.eof() {
		return nil, nil
	}
	switch sc.peek() {
	case 'Z', 'z':
		sc.i++
		return time.UTC, nil
	case '+', '-':
		sign := 1
		if sc.peek() == '-' {
			sign = -1
		}
		sc.i++
		h, ok := sc.digits(2)
		if !ok {
			return nil, parseErr("invalid offset hours")
		}
		min := 0
		if sc.peek() == ':' {
			sc.i++
			if min, ok = sc.digits(2); !ok {
				return nil, parseErr("invalid offset minutes")
			}
		} else if sc.peek() >= '0' && sc.peek() <= '9' {
			if min, ok = sc.digits(2); !ok {
				return nil, parseErr("invalid offset minutes")
			}
		}
		off := sign * (h*3600 + min*60)
		if h > 23 || min > 59 || off < -86399 || off > 86399 {
			return nil, parseErr("time zone offset out of range")
		}
		return time.FixedZone(formatOffsetName(off), off), nil
	case '[':
		sc.i++
		end := strings.IndexByte(sc.s[sc.i:], ']')
		if end < 0 {
			return nil, parseErr("unterminated '[' time zone")
		}
		name := sc.s[sc.i : sc.i+end]
		sc.i += end + 1
		return loadNamedZone(name)
	case ' ':
		// Bare IANA name after a space: "2024-01-01T00:00:00 Asia/Shanghai".
		start := sc.i + 1
		name := strings.TrimSpace(sc.s[start:])
		if name == "" {
			return nil, nil
		}
		loc, err := loadNamedZone(name)
		if err != nil {
			return nil, err
		}
		sc.i = len(sc.s)
		return loc, nil
	default:
		return nil, parseErr("unrecognized time zone suffix")
	}
}

func loadNamedZone(name string) (*time.Location, error) {
	if name == "UTC" || name == "GMT" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, parseErr("unknown time zone " + name)
	}
	return loc, nil
}

func formatOffsetName(off int) string {
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	h := off / 3600
	m := (off % 3600) / 60
	var b strings.Builder
	b.WriteByte(byte(sign))
	write2 := func(v int) {
		b.WriteByte(byte('0' + v/10))
		b.WriteByte(byte('0' + v%10))
	}
	write2(h)
	b.WriteByte(':')
	write2(m)
	return b.String()
}

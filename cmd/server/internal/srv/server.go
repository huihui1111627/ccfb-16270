// Package srv exposes the tempora engine over HTTP and serves the single
// page that visualizes cross-zone instants and DST transition days.
package srv

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tempora"

	_ "time/tzdata" // deterministic rules, independent of the host zoneinfo
)

// curatedZones is the zone picker's default list. Any other IANA name still
// works when typed explicitly.
var curatedZones = []string{
	"UTC", "America/New_York", "America/Chicago", "America/Denver",
	"America/Los_Angeles", "America/Sao_Paulo", "America/Havana",
	"Europe/London", "Europe/Berlin", "Europe/Athens", "Europe/Moscow",
	"Africa/Cairo", "Africa/Johannesburg", "Asia/Tehran", "Asia/Dubai",
	"Asia/Karachi", "Asia/Kolkata", "Asia/Shanghai", "Asia/Singapore",
	"Asia/Tokyo", "Asia/Seoul", "Australia/Perth", "Australia/Adelaide",
	"Australia/Sydney", "Australia/Lord_Howe", "Pacific/Auckland",
	"Pacific/Apia", "Pacific/Honolulu",
}

// Server holds no mutable request state; every request carries its zone.
type Server struct {
	mux *http.ServeMux
}

// New wires the routes. static is the HTML/JS/CSS payload (embedded).
func New(static []byte) *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(static)
	})
	s.mux.HandleFunc("/api/zones", s.handleZones)
	s.mux.HandleFunc("/api/instant", s.handleInstant)
	s.mux.HandleFunc("/api/timeline", s.handleTimeline)
	s.mux.HandleFunc("/api/report", s.handleReport)
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleZones(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"zones": curatedZones})
}

// instantView is one zone's rendering of a single instant.
type instantView struct {
	Zone      string `json:"zone"`
	Wall      string `json:"wall"`
	Offset    string `json:"offset"`
	OffsetSec int    `json:"offsetSeconds"`
	Abbr      string `json:"abbreviation"`
	UnixNano  int64  `json:"unixNano"`
}

func (s *Server) handleInstant(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	baseZone := q.Get("baseZone")
	wall := q.Get("wall")
	targets := splitCSV(q.Get("zones"))
	if baseZone == "" || wall == "" || len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "baseZone, wall and zones are required")
		return
	}
	sess, err := tempora.NewSession(baseZone)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ldt, err := tempora.ParseLocalDateTime(wall)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ts, err := sess.LocalToTimestamp(ldt)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	views := make([]instantView, 0, len(targets))
	for _, z := range targets {
		zSess, err := tempora.NewSession(z)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "unknown zone "+z)
			return
		}
		local := zSess.TimestampToLocal(ts)
		views = append(views, instantView{
			Zone: z, Wall: tempora.ISOLocal(local),
			Offset:    offsetString(zSess.OffsetSeconds(ts)),
			OffsetSec: zSess.OffsetSeconds(ts),
			Abbr:      zSess.ZoneAbbr(ts),
			UnixNano:  ts.UnixNano(),
		})
	}
	writeJSON(w, map[string]any{
		"instantUTC": tempora.DefaultSession(time.UTC).ISOTimestamp(ts),
		"unixNano":   ts.UnixNano(),
		"views":      views,
	})
}

type hourView struct {
	Wall    string `json:"wall"`
	Kind    string `json:"kind"`
	Start   string `json:"startUTC,omitempty"`
	End     string `json:"endUTC,omitempty"`
	Offset  int    `json:"offsetSeconds,omitempty"`
	RealMin int64  `json:"realSpanMinutes,omitempty"`
	Count   int64  `json:"count"`
	Sum     *int64 `json:"sum"`
}

type transitionView struct {
	AtUTC         string     `json:"atUTC"`
	OldOffset     int        `json:"oldOffsetSeconds"`
	NewOffset     int        `json:"newOffsetSeconds"`
	OldAbbr       string     `json:"oldAbbr"`
	NewAbbr       string     `json:"newAbbr"`
	GapWindow     *[2]string `json:"gapWindow,omitempty"`
	OverlapWindow *[2]string `json:"overlapWindow,omitempty"`
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("zone")
	date := r.URL.Query().Get("date")
	if zone == "" || date == "" {
		writeErr(w, http.StatusBadRequest, "zone and date (YYYY-MM-DD) are required")
		return
	}
	sess, err := tempora.NewSession(zone)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := tempora.ParseDate(date)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hours, err := sess.LocalTimeline(d)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	hv := make([]hourView, 0, len(hours))
	for _, h := range hours {
		v := hourView{Wall: tempora.ISOLocal(h.Wall), Kind: h.Kind.String(), Offset: h.Offset}
		if h.Kind != tempora.WallGap {
			v.Start = h.Start.Time().Format(time.RFC3339Nano)
			v.End = h.End.Time().Format(time.RFC3339Nano)
			v.RealMin = int64(h.End.Time().Sub(h.Start.Time()).Minutes())
		}
		hv = append(hv, v)
	}
	trs, err := sess.TransitionsOn(d)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	tv := make([]transitionView, 0, len(trs))
	for _, tr := range trs {
		out := transitionView{
			AtUTC:     tr.AtUTC.Time().Format(time.RFC3339Nano),
			OldOffset: tr.OldOffset, NewOffset: tr.NewOffset,
			OldAbbr: tr.OldAbbr, NewAbbr: tr.NewAbbr,
		}
		if tr.GapWindow != nil {
			out.GapWindow = &[2]string{tempora.ISOLocal(tr.GapWindow.From), tempora.ISOLocal(tr.GapWindow.To)}
		}
		if tr.OverlapWindow != nil {
			out.OverlapWindow = &[2]string{tempora.ISOLocal(tr.OverlapWindow.From), tempora.ISOLocal(tr.OverlapWindow.To)}
		}
		tv = append(tv, out)
	}
	writeJSON(w, map[string]any{"zone": zone, "date": date, "hours": hv, "transitions": tv})
}

// handleReport runs the canonical hourly report over one local day and returns
// the full spine — gap rows carry null measures, overlap rows appear twice.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("zone")
	date := r.URL.Query().Get("date")
	if zone == "" || date == "" {
		writeErr(w, http.StatusBadRequest, "zone and date (YYYY-MM-DD) are required")
		return
	}
	sess, err := tempora.NewSession(zone)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := tempora.ParseDate(date)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	start, err := sess.LocalDateAtMidnight(d)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	nextDay := d.AddDays(1)
	end, err := sess.LocalDateAtMidnight(nextDay)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Deterministic demo events: one per whole real hour, measure = its
	// UTC hour + 1. This makes every gap/overlap behavior visible.
	type E = struct {
		TS tempora.Timestamp
		V  int64
	}
	var events []E
	for cur := start.Time(); cur.Before(end.Time()); cur = cur.Add(time.Hour) {
		events = append(events, E{tempora.TimestampFromTime(cur), int64(cur.UTC().Hour()) + 1})
	}
	rows, err := sess.AggregateHourly[int64](start, end, events)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	hv := make([]hourView, 0, len(rows))
	var totalCount int64
	var totalSum int64
	for _, row := range rows {
		v := hourView{Wall: tempora.ISOLocal(row.Wall), Kind: row.Kind.String(), Count: row.Count}
		if row.Kind != tempora.BucketGap {
			v.Start = row.Start.Time().Format(time.RFC3339Nano)
			v.End = row.End.Time().Format(time.RFC3339Nano)
			v.Offset = row.Offset
			v.RealMin = int64(row.RealSpan.Minutes())
		}
		if row.Has {
			sum := row.Sum
			v.Sum = &sum
			totalCount += row.Count
			totalSum += row.Sum
		}
		hv = append(hv, v)
	}
	writeJSON(w, map[string]any{
		"zone": zone, "date": date, "rows": hv,
		"rowCount":   len(hv),
		"totalCount": totalCount,
		"totalSum":   totalSum,
	})
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func offsetString(sec int) string {
	sign := '+'
	if sec < 0 {
		sign = '-'
		sec = -sec
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	return string(sign) + pad(h) + ":" + pad(m)
}

func pad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

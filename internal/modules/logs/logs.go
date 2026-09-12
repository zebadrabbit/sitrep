// Package logs shows recent journal errors, unit-colored (HANDOFF §5 #11).
package logs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Entry is one journal record.
type Entry struct {
	Time     time.Time `json:"time"`
	Priority int       `json:"priority"`
	Unit     string    `json:"unit"`
	Message  string    `json:"message"`
}

// Row is an Entry with consecutive repeats folded in.
type Row struct {
	Entry
	Count int `json:"count"`
}

// Rate is one unit's error count over three trailing windows.
type Rate struct {
	Unit     string `json:"unit"`
	M5       int    `json:"m5"`
	M15      int    `json:"m15"`
	H1       int    `json:"h1"`
	Building bool   `json:"building"` // 5-minute rate is well above the hour average
}

// Data is one collection.
type Data struct {
	Entries  []Row          `json:"entries"`   // collapsed tail, newest last
	LastHour int            `json:"last_hour"` // errors in the past hour (capped at hourCap)
	ByUnit   map[string]int `json:"by_unit"`   // last-hour counts
	Top      string         `json:"top,omitempty"`
	Rates    []Rate         `json:"rates"` // per unit, busiest first
}

// hourCap bounds the hour fetch; a unit flapping every 5s is 720/h on its own.
const hourCap = 2000

type Module struct {
	run *collect.Runner
}

func New(demo bool) *Module { return &Module{run: collect.New("logs", demo)} }

func (*Module) ID() string              { return "logs" }
func (*Module) Title() string           { return "Logs" }
func (*Module) Flags() module.Flags     { return module.Flags{Slow: true} } // journalctl over an hour can take ~1s
func (*Module) Interval() time.Duration { return 10 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: journal entries at priority err or worse from the past hour,
counted per unit over the last 5m / 15m / 1h so a unit whose errors are
building (+) stands out from one that is merely noisy. Consecutive repeats
of the same message fold into one line with a ×N count. Falls back to the
last 50 entries when the hour is quiet. Units are colored by a stable hash
so the same unit is always the same color.
Needs:    journalctl and read access to the system journal (adm or
systemd-journal group, or root); otherwise only your own user journal shows.
Execs:    journalctl -p err --since -1h -n 2000 -o json --no-pager
          journalctl -p err -n 50 -o json --no-pager
Interval: 10s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("journalctl") {
		return module.Availability{State: module.Missing, Reason: "journalctl missing (systemd)"}
	}
	return module.Availability{State: module.Available, Reason: "journalctl"}
}

type rec struct {
	TS       string `json:"__REALTIME_TIMESTAMP"`
	Priority string `json:"PRIORITY"`
	Unit     string `json:"_SYSTEMD_UNIT"`
	About    string `json:"UNIT"` // systemd's own messages name the unit they are about
	Ident    string `json:"SYSLOG_IDENTIFIER"`
	Comm     string `json:"_COMM"`
	Message  any    `json:"MESSAGE"` // journald emits a byte array for non-UTF8
}

// ParseJournal reads newline-delimited JSON records; bad lines are skipped.
func ParseJournal(out []byte) []Entry {
	var es []Entry
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var r rec
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		us, _ := strconv.ParseInt(r.TS, 10, 64)
		p, _ := strconv.Atoi(r.Priority)
		unit := r.About // "Failed to start foo.service" belongs to foo, not init.scope
		if unit == "" {
			unit = r.Unit
		}
		if unit == "" {
			unit = r.Ident
		}
		if unit == "" {
			unit = r.Comm
		}
		msg, _ := r.Message.(string)
		if msg == "" {
			msg = "(binary message)"
		}
		es = append(es, Entry{Time: time.UnixMicro(us), Priority: p, Unit: strings.TrimSuffix(unit, ".service"), Message: msg})
	}
	return es
}

// Collapse folds consecutive entries with the same unit and message into one
// Row that keeps the latest time, so a flapping unit takes one line.
func Collapse(es []Entry) []Row {
	var rows []Row
	for _, e := range es {
		if n := len(rows); n > 0 && rows[n-1].Unit == e.Unit && rows[n-1].Message == e.Message {
			rows[n-1].Count++
			rows[n-1].Time = e.Time
			continue
		}
		rows = append(rows, Row{Entry: e, Count: 1})
	}
	return rows
}

// Rates counts errors per unit in the 5m/15m/1h windows ending at now,
// busiest first. Entries older than an hour are ignored.
func Rates(es []Entry, now time.Time) []Rate {
	idx := map[string]int{}
	var rs []Rate
	for _, e := range es {
		age := now.Sub(e.Time)
		if age > time.Hour || age < 0 {
			continue
		}
		i, ok := idx[e.Unit]
		if !ok {
			i = len(rs)
			idx[e.Unit] = i
			rs = append(rs, Rate{Unit: e.Unit})
		}
		rs[i].H1++
		if age <= 15*time.Minute {
			rs[i].M15++
		}
		if age <= 5*time.Minute {
			rs[i].M5++
		}
	}
	for i := range rs {
		// ponytail: "building" = last 5m projected over an hour beats the hour by 25%; tune the margin if it flickers
		rs[i].Building = rs[i].M5*12 > rs[i].H1+rs[i].H1/4
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].H1 > rs[j].H1 })
	return rs
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	hr, err := m.run.Run(ctx, "journalctl", "-p", "err", "--since", "-1h", "-n", fmt.Sprint(hourCap), "-o", "json", "--no-pager")
	if err != nil {
		return nil, err
	}
	hour := ParseJournal(hr.Stdout)
	tail := hour
	if len(hour) == 0 {
		// Quiet hour: show the last 50 so "latest 3d ago" still means something.
		if res, err := m.run.Run(ctx, "journalctl", "-p", "err", "-n", "50", "-o", "json", "--no-pager"); err == nil {
			tail = ParseJournal(res.Stdout)
		}
	}
	now := time.Now()
	if m.run.Demo && len(hour) > 0 {
		now = hour[len(hour)-1].Time // fixtures are frozen; anchor the windows to their newest entry
	}
	d := Data{Entries: Collapse(tail), LastHour: len(hour), ByUnit: map[string]int{}, Rates: Rates(hour, now)}
	for _, e := range hour {
		d.ByUnit[e.Unit]++
	}
	if len(d.Rates) > 0 {
		d.Top = d.Rates[0].Unit
	}
	return d, nil
}

// unitStyle picks one of four theme styles by hash, so a unit keeps its
// color across refreshes. Colors come from theme only (HANDOFF §10.5).
func unitStyle(unit string) lipgloss.Style {
	s := theme.Current()
	h := fnv.New32a()
	_, _ = h.Write([]byte(unit))
	return []lipgloss.Style{s.Accent, s.OK, s.Port, s.Warn}[h.Sum32()%4]
}

func (*Module) Card(d module.Data, w int) string {
	ld, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	n := s.Bold.Render(fmt.Sprint(ld.LastHour))
	if ld.LastHour >= hourCap {
		n = s.Bold.Render(fmt.Sprintf("%d+", hourCap))
	}
	line := n + " errors last hour"
	if ld.LastHour == 0 {
		line = s.OK.Render(s.Glyph.OK) + " no errors last hour"
	}
	lines := []string{line}
	if len(ld.Rates) > 0 {
		top := ld.Rates[0]
		g := s.Warn.Render(s.Glyph.Warn)
		if top.Building {
			g = s.Crit.Render(s.Glyph.New) // rising, not just noisy; the tab has the 5m/15m/1h numbers
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s", g, unitStyle(top.Unit).Render(top.Unit), s.Dim.Render(fmt.Sprintf("%d of them", top.H1))))
	}
	if n := len(ld.Entries); n > 0 {
		last := ld.Entries[n-1]
		lines = append(lines, s.Dim.Render("latest "+ui.Age(time.Since(last.Time))+" ago: ")+clip(last.Message, max(10, w-24)))
	}
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	ld, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	out := []string{fmt.Sprintf("%s errors last hour", s.Bold.Render(fmt.Sprint(ld.LastHour)))}
	if len(ld.Rates) > 0 {
		out = append(out, rateTable(ld.Rates, w), "")
	}
	rows := make([]string, 0, len(ld.Entries))
	for _, e := range ld.Entries {
		g := s.Warn.Render(s.Glyph.Warn)
		if e.Priority <= 2 {
			g = s.Crit.Render(s.Glyph.Crit)
		}
		n := ""
		if e.Count > 1 {
			n = s.Bold.Render(fmt.Sprintf("×%d ", e.Count))
		}
		line := fmt.Sprintf("%s %s %s %s%s", s.Dim.Render(e.Time.Format("15:04:05")), g, unitStyle(e.Unit).Render(clip(e.Unit, 24)), n, e.Message)
		rows = append(rows, lipgloss.NewStyle().MaxWidth(w).Render(line))
	}
	// Newest at the bottom, like a tail; window to h from the end.
	if room := h - len(out) - 1; h > 0 && len(rows) > room {
		rows = rows[len(rows)-max(room, 0):]
	}
	return strings.Join(append(out, rows...), "\n")
}

// rateTable is the top five units by hour count with their 5m/15m/1h
// counts; a building unit gets the + glyph so a rising rate reads at a glance.
func rateTable(rs []Rate, w int) string {
	s := theme.Current()
	rows := make([][]string, 0, 5)
	for i, r := range rs {
		if i == 5 {
			break
		}
		trend := " "
		if r.Building {
			trend = s.Crit.Render(s.Glyph.New)
		}
		rows = append(rows, []string{trend, unitStyle(r.Unit).Render(clip(r.Unit, 24)), fmt.Sprint(r.M5), fmt.Sprint(r.M15), fmt.Sprint(r.H1)})
	}
	return ui.Table([]string{" ", "unit", "5m", "15m", "1h"}, rows, w)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

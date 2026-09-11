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
)

// Entry is one journal record.
type Entry struct {
	Time     time.Time `json:"time"`
	Priority int       `json:"priority"`
	Unit     string    `json:"unit"`
	Message  string    `json:"message"`
}

// Data is one collection.
type Data struct {
	Entries  []Entry        `json:"entries"`   // last 50, newest last
	LastHour int            `json:"last_hour"` // errors in the past hour (capped at 1000)
	ByUnit   map[string]int `json:"by_unit"`   // last-hour counts
	Top      string         `json:"top,omitempty"`
}

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
	return `Collects: the last 50 journal entries at priority err or worse, and how
many there were in the past hour (by unit). Units are colored by a stable
hash so the same unit is always the same color.
Needs:    journalctl and read access to the system journal (adm or
systemd-journal group, or root); otherwise only your own user journal shows.
Execs:    journalctl -p err -n 50 -o json --no-pager
          journalctl -p err --since -1h -n 1000 -o json --no-pager
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

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, "journalctl", "-p", "err", "-n", "50", "-o", "json", "--no-pager")
	if err != nil {
		return nil, err
	}
	d := Data{Entries: ParseJournal(res.Stdout), ByUnit: map[string]int{}}
	if hr, err := m.run.Run(ctx, "journalctl", "-p", "err", "--since", "-1h", "-n", "1000", "-o", "json", "--no-pager"); err == nil {
		for _, e := range ParseJournal(hr.Stdout) {
			d.LastHour++
			d.ByUnit[e.Unit]++
		}
	}
	best := 0
	for u, n := range d.ByUnit {
		if n > best {
			best, d.Top = n, u
		}
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
	if ld.LastHour >= 1000 {
		n = s.Bold.Render("1000+")
	}
	line := n + " errors last hour"
	if ld.LastHour == 0 {
		line = s.OK.Render(s.Glyph.OK) + " no errors last hour"
	}
	lines := []string{line}
	if ld.Top != "" {
		lines = append(lines, fmt.Sprintf("%s %s  %s", s.Warn.Render(s.Glyph.Warn), unitStyle(ld.Top).Render(ld.Top), s.Dim.Render(fmt.Sprintf("%d of them", ld.ByUnit[ld.Top]))))
	}
	if n := len(ld.Entries); n > 0 {
		last := ld.Entries[n-1]
		lines = append(lines, s.Dim.Render("latest "+ui_age(time.Since(last.Time))+" ago: ")+clip(last.Message, max(10, w-24)))
	}
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	ld, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	units := make([]string, 0, len(ld.ByUnit))
	for u := range ld.ByUnit {
		units = append(units, u)
	}
	sort.Slice(units, func(i, j int) bool { return ld.ByUnit[units[i]] > ld.ByUnit[units[j]] })
	head := []string{}
	for i, u := range units {
		if i == 5 {
			break
		}
		head = append(head, unitStyle(u).Render(u)+s.Dim.Render(fmt.Sprintf(" %d", ld.ByUnit[u])))
	}
	out := []string{fmt.Sprintf("%s errors last hour   %s", s.Bold.Render(fmt.Sprint(ld.LastHour)), strings.Join(head, "  ")), ""}
	rows := make([]string, 0, len(ld.Entries))
	for _, e := range ld.Entries {
		g := s.Warn.Render(s.Glyph.Warn)
		if e.Priority <= 2 {
			g = s.Crit.Render(s.Glyph.Crit)
		}
		line := fmt.Sprintf("%s %s %s %s", s.Dim.Render(e.Time.Format("15:04:05")), g, unitStyle(e.Unit).Render(clip(e.Unit, 24)), e.Message)
		rows = append(rows, lipgloss.NewStyle().MaxWidth(w).Render(line))
	}
	// Newest at the bottom, like a tail; window to h from the end.
	if h > 0 && len(rows) > h-2 {
		rows = rows[len(rows)-(h-2):]
	}
	return strings.Join(append(out, rows...), "\n")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ui_age mirrors ui.Age without importing ui (keeps this package light).
func ui_age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

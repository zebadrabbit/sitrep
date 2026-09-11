// Package cron shows scheduled work: systemd timers with their last result,
// crontab entries from every crontab location, and the periodic dirs.
// Read-only, like everything else.
package cron

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Timer is one systemd timer joined with its service's last result.
type Timer struct {
	Unit      string    `json:"unit"`
	Activates string    `json:"activates"`
	Next      time.Time `json:"next,omitempty"`
	Last      time.Time `json:"last,omitempty"`
	Result    string    `json:"result,omitempty"` // success, exit-code, timeout, …
}

// Job is one crontab line.
type Job struct {
	Source   string `json:"source"` // /etc/crontab, /etc/cron.d/pihole, crontab:winter
	Schedule string `json:"schedule"`
	User     string `json:"user"`
	Command  string `json:"command"`
}

// Data is one collection.
type Data struct {
	Timers         []Timer             `json:"timers"`
	Jobs           []Job               `json:"jobs"`
	Periodic       map[string][]string `json:"periodic"` // cron.daily → scripts
	Failed         int                 `json:"failed"`   // timers whose last run failed
	SpoolNeedsRoot bool                `json:"spool_needs_root"`
	Collected      time.Time           `json:"collected"`
}

type Module struct {
	run  *collect.Runner
	demo bool
	root bool
}

func New(demo bool) *Module { return &Module{run: collect.New("cron", demo), demo: demo} }

func (*Module) ID() string              { return "cron" }
func (*Module) Title() string           { return "Cron" }
func (*Module) Flags() module.Flags     { return module.Flags{NeedsRootForFull: true} }
func (*Module) Interval() time.Duration { return 30 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: systemd timers (next, last, and the activated service's last
Result), crontab entries from /etc/crontab, /etc/cron.d/*, your own crontab
(crontab -l) and, as root, every user's spool under /var/spool/cron/crontabs,
plus the scripts in /etc/cron.{hourly,daily,weekly,monthly}.
Needs:    systemctl for timers; cron files are plain reads. Other users'
spools need root (◐ otherwise).
Execs:    systemctl list-timers --all --output=json, systemctl show (results),
crontab -l.
Interval: 30s.`
}

func (m *Module) Detect(_ context.Context, env detect.Env) module.Availability {
	m.root = env.Root
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("systemctl") && !detect.Has("crontab") {
		return module.Availability{State: module.Missing, Reason: "neither systemctl nor crontab found"}
	}
	if !env.Root {
		return module.Availability{State: module.NeedsRoot, Reason: "other users' crontabs need root"}
	}
	return module.Availability{State: module.Available, Reason: "systemd timers + crontabs"}
}

type timerRow struct {
	Unit      string `json:"unit"`
	Activates string `json:"activates"`
	Next      *int64 `json:"next"`
	Last      *int64 `json:"last"`
}

// ParseTimers decodes list-timers JSON (microsecond timestamps, null when never).
func ParseTimers(out []byte) ([]Timer, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	var rows []timerRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("cron: parse list-timers: %w", err)
	}
	ts := make([]Timer, 0, len(rows))
	for _, r := range rows {
		t := Timer{Unit: r.Unit, Activates: r.Activates}
		if r.Next != nil && *r.Next > 0 {
			t.Next = time.UnixMicro(*r.Next)
		}
		if r.Last != nil && *r.Last > 0 {
			t.Last = time.UnixMicro(*r.Last)
		}
		ts = append(ts, t)
	}
	return ts, nil
}

// ParseShow reads `systemctl show -p Id,Result` blocks into unit → Result.
func ParseShow(out []byte) map[string]string {
	res := map[string]string{}
	id, result := "", ""
	flush := func() {
		if id != "" {
			res[id] = result
		}
		id, result = "", ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "Id="):
			id = strings.TrimPrefix(line, "Id=")
		case strings.HasPrefix(line, "Result="):
			result = strings.TrimPrefix(line, "Result=")
		}
	}
	flush()
	return res
}

// ParseCrontab reads crontab lines. withUser is true for system crontabs
// (/etc/crontab, /etc/cron.d) which carry a user column.
func ParseCrontab(out []byte, source, user string, withUser bool) []Job {
	var jobs []Job
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "="); i > 0 && !strings.ContainsAny(line[:i], " \t*") {
			continue // SHELL=, PATH=, MAILTO=
		}
		f := strings.Fields(line)
		n := 5
		if strings.HasPrefix(line, "@") {
			n = 1
		}
		need := n + 1
		if withUser {
			need++
		}
		if len(f) < need {
			continue
		}
		j := Job{Source: source, Schedule: strings.Join(f[:n], " "), User: user}
		rest := f[n:]
		if withUser {
			j.User, rest = rest[0], rest[1:]
		}
		j.Command = strings.Join(rest, " ")
		jobs = append(jobs, j)
	}
	return jobs
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	d := Data{Periodic: map[string][]string{}, Collected: time.Now()}
	if err := m.timers(ctx, &d); err != nil {
		return nil, err
	}
	m.crontabs(ctx, &d)
	sort.Slice(d.Jobs, func(i, j int) bool {
		if d.Jobs[i].Source != d.Jobs[j].Source {
			return d.Jobs[i].Source < d.Jobs[j].Source
		}
		return d.Jobs[i].Command < d.Jobs[j].Command
	})
	return d, nil
}

func (m *Module) timers(ctx context.Context, d *Data) error {
	res, err := m.run.Run(ctx, "systemctl", "list-timers", "--all", "--output=json", "--no-pager")
	if errors.Is(err, collect.ErrMissing) || errors.Is(err, collect.ErrNoFixture) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Timers, err = ParseTimers(res.Stdout); err != nil {
		return err
	}
	units := make([]string, 0, len(d.Timers))
	for _, t := range d.Timers {
		if t.Activates != "" {
			units = append(units, t.Activates)
		}
	}
	if len(units) > 0 {
		args := append([]string{"show", "-p", "Id,Result"}, units...)
		if show, err := m.run.RunNamed(ctx, "systemctl_show_timers.txt", "systemctl", args...); err == nil {
			results := ParseShow(show.Stdout)
			for i := range d.Timers {
				d.Timers[i].Result = results[d.Timers[i].Activates]
				if r := d.Timers[i].Result; r != "" && r != "success" && !d.Timers[i].Last.IsZero() {
					d.Failed++
				}
			}
		}
	}
	// Soonest first; never-scheduled last.
	sort.SliceStable(d.Timers, func(i, j int) bool {
		a, b := d.Timers[i].Next, d.Timers[j].Next
		if a.IsZero() != b.IsZero() {
			return !a.IsZero()
		}
		return a.Before(b)
	})
	return nil
}

func (m *Module) crontabs(ctx context.Context, d *Data) {
	if b, err := m.run.ReadFile("/etc/crontab"); err == nil {
		d.Jobs = append(d.Jobs, ParseCrontab(b, "/etc/crontab", "", true)...)
	}
	for _, f := range m.run.Glob("/etc/cron.d/*") {
		if strings.HasSuffix(f, ".dpkg-dist") || strings.HasSuffix(f, "~") {
			continue
		}
		if b, err := m.run.ReadFile(f); err == nil {
			d.Jobs = append(d.Jobs, ParseCrontab(b, f, "", true)...)
		}
	}
	for _, dir := range []string{"hourly", "daily", "weekly", "monthly"} {
		for _, f := range m.run.Glob("/etc/cron." + dir + "/*") {
			d.Periodic[dir] = append(d.Periodic[dir], filepath.Base(f))
		}
	}
	// Per-user spools: readable as root; otherwise only our own via crontab -l.
	spools := m.run.Glob("/var/spool/cron/crontabs/*")
	if len(spools) > 0 {
		for _, f := range spools {
			if b, err := m.run.ReadFile(f); err == nil {
				d.Jobs = append(d.Jobs, ParseCrontab(b, "crontab:"+filepath.Base(f), filepath.Base(f), false)...)
			}
		}
	} else if !m.root && !m.demo {
		d.SpoolNeedsRoot = true
		if res, err := m.run.Run(ctx, "crontab", "-l"); err == nil {
			d.Jobs = append(d.Jobs, ParseCrontab(res.Stdout, "crontab:me", "me", false)...)
		}
	}
}

func (*Module) Card(d module.Data, w int) string {
	cd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	line := fmt.Sprintf("%s timers  %s cron jobs", s.Bold.Render(fmt.Sprint(len(cd.Timers))), s.Bold.Render(fmt.Sprint(len(cd.Jobs))))
	if cd.Failed > 0 {
		line += "  " + s.Crit.Render(fmt.Sprintf("%s %d failed", s.Glyph.Fail, cd.Failed))
	}
	lines := []string{line}
	for _, t := range cd.Timers {
		if !t.Next.IsZero() {
			lines = append(lines, fmt.Sprintf("next  %s  %s", strings.TrimSuffix(t.Unit, ".timer"), s.Dim.Render("in "+ui.Age(t.Next.Sub(cd.Collected)))))
			break
		}
	}
	for _, t := range cd.Timers {
		if t.Result != "" && t.Result != "success" && !t.Last.IsZero() && len(lines) < 4 {
			lines = append(lines, fmt.Sprintf("%s %s  %s", s.Crit.Render(s.Glyph.Fail), strings.TrimSuffix(t.Activates, ".service"), s.Dim.Render(t.Result)))
		}
	}
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	cd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	rows := [][]string{}
	for _, t := range cd.Timers {
		next, last := s.Dim.Render("—"), s.Dim.Render("never")
		if !t.Next.IsZero() {
			next = "in " + ui.Age(t.Next.Sub(cd.Collected))
		}
		if !t.Last.IsZero() {
			last = ui.Age(cd.Collected.Sub(t.Last)) + " ago"
		}
		g := s.OK.Render(s.Glyph.OK)
		res := s.Dim.Render(t.Result)
		switch {
		case t.Last.IsZero():
			g = s.Dim.Render(s.Glyph.Fail)
			res = ""
		case t.Result != "success" && t.Result != "":
			g = s.Crit.Render(s.Glyph.Fail)
			res = s.Crit.Render(t.Result)
		}
		rows = append(rows, []string{g + " " + strings.TrimSuffix(t.Unit, ".timer"), next, last, res})
	}
	out := []string{s.Bold.Render(fmt.Sprintf("systemd timers (%d)", len(cd.Timers))), ui.Table([]string{"  TIMER", "NEXT", "LAST", "RESULT"}, rows, w), ""}
	jrows := [][]string{}
	for _, j := range cd.Jobs {
		jrows = append(jrows, []string{j.Schedule, j.User, clip(j.Command, max(20, w-62)), s.Dim.Render(strings.TrimPrefix(j.Source, "/etc/"))})
	}
	title := s.Bold.Render(fmt.Sprintf("crontabs (%d)", len(cd.Jobs)))
	if cd.SpoolNeedsRoot {
		title += "  " + s.Warn.Render(s.Glyph.Degraded+" other users' crontabs need root")
	}
	out = append(out, title)
	if len(jrows) == 0 {
		out = append(out, s.Dim.Render("  none"))
	} else {
		out = append(out, ui.Table([]string{"SCHEDULE", "USER", "COMMAND", "SOURCE"}, jrows, w))
	}
	per := []string{}
	for _, dir := range []string{"hourly", "daily", "weekly", "monthly"} {
		if n := len(cd.Periodic[dir]); n > 0 {
			per = append(per, fmt.Sprintf("%s %s", s.Dim.Render(dir), strings.Join(cd.Periodic[dir], " ")))
		}
	}
	if len(per) > 0 {
		out = append(out, "", s.Bold.Render("periodic")+"  "+strings.Join(per, "   "))
	}
	res := strings.Join(out, "\n")
	if h > 0 {
		res = strings.Join(strings.Split(res, "\n")[:min(h, strings.Count(res, "\n")+1)], "\n")
	}
	return res
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

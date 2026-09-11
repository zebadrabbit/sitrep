// Package updates shows what is pending and how the owner's update script
// last went. Read-only: it never runs anything (HANDOFF §1). Running
// update-all.sh is parked under v2 in docs/DECISIONS.md.
package updates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Data is one collection.
type Data struct {
	Packages       []Pkg     `json:"packages"`
	Security       int       `json:"security"`
	AptRefreshed   time.Time `json:"apt_refreshed"`
	RebootRequired bool      `json:"reboot_required"`
	RebootPkgs     []string  `json:"reboot_pkgs,omitempty"`
	Kernel         string    `json:"kernel"`
	NewestKernel   string    `json:"newest_kernel"`
	KernelStale    bool      `json:"kernel_stale"`
	LogPath        string    `json:"log_path"`
	Runs           int       `json:"runs"`
	LastRun        *Run      `json:"last_run,omitempty"`
	Collected      time.Time `json:"collected"`
}

type Module struct {
	run  *collect.Runner
	demo bool
	log  string
}

// New reads the log path from config; ~ expands. Demo uses a fixed fixture path.
func New(demo bool) *Module {
	m := &Module{run: collect.New("updates", demo), demo: demo}
	cfg, _, _ := config.Load()
	m.log = cfg.UpdatesLog
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(m.log, "~/") {
		m.log = filepath.Join(home, m.log[2:])
	}
	if demo {
		m.log = "/update-all.log"
	}
	return m
}

func (*Module) ID() string              { return "updates" }
func (*Module) Title() string           { return "Updates" }
func (*Module) Flags() module.Flags     { return module.Flags{Slow: true} }
func (*Module) Interval() time.Duration { return time.Hour } // apt list is cached hourly (HANDOFF §5)
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: apt packages with upgrades pending (security ones flagged), when apt
lists were last refreshed, whether a reboot is required, running vs newest
installed kernel, and the last run of the owner's update script parsed from
its log (steps, failures, reboot flag).
Needs:    apt (Debian family). Log path from config key updates_log
(default ~/update-all.log); absent log = "no runs recorded".
Execs:    apt list --upgradable. Reads /var/run/reboot-required*,
/var/lib/apt/periodic/update-success-stamp, /boot/vmlinuz-*, /proc/sys/kernel/osrelease.
Never runs updates — v1 is read-only. r forces a refresh.
Interval: 1h.`
}

func (m *Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("apt") {
		return module.Availability{State: module.Unsupported, Reason: "no apt (Debian family only in v1)"}
	}
	return module.Availability{State: module.Available, Reason: "apt"}
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	d := Data{LogPath: m.log, Collected: time.Now()}
	res, err := m.run.Run(ctx, "apt", "list", "--upgradable")
	if err != nil && !errors.Is(err, collect.ErrNoFixture) {
		return nil, err
	}
	d.Packages = ParseAptList(res.Stdout)
	sort.Slice(d.Packages, func(i, j int) bool {
		if d.Packages[i].Security != d.Packages[j].Security {
			return d.Packages[i].Security
		}
		return d.Packages[i].Name < d.Packages[j].Name
	})
	for _, p := range d.Packages {
		if p.Security {
			d.Security++
		}
	}
	d.AptRefreshed, _ = m.run.ModTime("/var/lib/apt/periodic/update-success-stamp")
	if _, err := m.run.ReadFile("/var/run/reboot-required"); err == nil {
		d.RebootRequired = true
		if b, err := m.run.ReadFile("/var/run/reboot-required.pkgs"); err == nil {
			d.RebootPkgs = strings.Fields(string(b))
		}
	}
	m.kernels(&d)
	if b, err := m.run.ReadFile(m.log); err == nil {
		runs := ParseLog(b)
		d.Runs = len(runs)
		if d.Runs > 0 {
			d.LastRun = &runs[d.Runs-1]
		}
	}
	return d, nil
}

func (m *Module) kernels(d *Data) {
	if b, err := m.run.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		d.Kernel = strings.TrimSpace(string(b))
	}
	d.NewestKernel = d.Kernel
	for _, p := range m.run.Glob("/boot/vmlinuz-*") {
		if v := kernelVersion(p); newerKernel(v, d.NewestKernel) {
			d.NewestKernel = v
		}
	}
	d.KernelStale = d.Kernel != "" && d.NewestKernel != d.Kernel
}

func (*Module) Card(d module.Data, w int) string {
	ud, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	pk := fmt.Sprintf("%s apt upgradable", s.Bold.Render(fmt.Sprint(len(ud.Packages))))
	if ud.Security > 0 {
		pk += "  " + s.Warn.Render(fmt.Sprintf("%s %d security", s.Glyph.Warn, ud.Security))
	}
	lines := []string{pk, rebootLine(ud)}
	if ud.LastRun != nil {
		lines = append(lines, "last run  "+runSummary(*ud.LastRun, ud.Collected))
	} else {
		lines = append(lines, s.Dim.Render("no update-all runs recorded"))
	}
	return strings.Join(lines, "\n")
}

func rebootLine(ud Data) string {
	s := theme.Current()
	switch {
	case ud.RebootRequired:
		return s.Warn.Render(s.Glyph.Warn + " reboot required")
	case ud.KernelStale:
		return s.Warn.Render(fmt.Sprintf("%s kernel %s installed, running %s", s.Glyph.Warn, ud.NewestKernel, ud.Kernel))
	}
	return s.OK.Render(s.Glyph.OK) + " no reboot needed"
}

func runSummary(r Run, now time.Time) string {
	s := theme.Current()
	out := ui.Age(now.Sub(r.Started)) + " ago"
	switch {
	case !r.Complete:
		out += "  " + s.Warn.Render(s.Glyph.Warn+" did not finish")
	case r.Failures > 0:
		out += "  " + s.Warn.Render(fmt.Sprintf("%s %d failed", s.Glyph.Warn, r.Failures))
	default:
		out += "  " + s.OK.Render(s.Glyph.OK+" clean")
	}
	return out
}

func (*Module) View(d module.Data, w, h int) string {
	ud, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	refreshed := s.Warn.Render(s.Glyph.Degraded + " unknown")
	if !ud.AptRefreshed.IsZero() {
		refreshed = ui.Age(ud.Collected.Sub(ud.AptRefreshed)) + " ago"
	}
	lines := []string{
		kv("apt lists refreshed", refreshed) + "   " + kv("kernel", ud.Kernel),
		rebootLine(ud),
		"",
		s.Bold.Render("last update-all run") + "  " + s.Dim.Render(ud.LogPath),
	}
	lines = append(lines, runLines(ud)...)
	lines = append(lines, "", s.Bold.Render(fmt.Sprintf("apt upgradable (%d)", len(ud.Packages))))
	rows := make([][]string, 0, len(ud.Packages))
	for _, p := range ud.Packages {
		mark := " "
		if p.Security {
			mark = s.Warn.Render(s.Glyph.Warn)
		}
		name := p.Name
		if p.Arch != "" && p.Arch != "amd64" && p.Arch != "arm64" && p.Arch != "all" {
			name += ":" + p.Arch // foreign-arch packages (i386 libc6) are otherwise duplicates
		}
		rows = append(rows, []string{mark + " " + name, p.From, "→ " + p.To, s.Dim.Render(p.Source)})
	}
	if len(rows) == 0 {
		lines = append(lines, s.Dim.Render("  nothing pending"))
	} else {
		lines = append(lines, ui.Table([]string{"  PACKAGE", "FROM", "TO", "SOURCE"}, rows, w))
	}
	out := strings.Join(lines, "\n")
	if h > 0 {
		out = strings.Join(strings.Split(out, "\n")[:min(h, strings.Count(out, "\n")+1)], "\n")
	}
	return out
}

func runLines(ud Data) []string {
	s := theme.Current()
	if ud.LastRun == nil {
		return []string{s.Dim.Render("  no runs recorded (set updates_log in config)")}
	}
	r := *ud.LastRun
	took := "unfinished"
	if r.Complete {
		took = "took " + ui.Age(r.Finished.Sub(r.Started))
	}
	out := []string{fmt.Sprintf("  %s  %s  %s  %s", r.Started.Format("2006-01-02 15:04"), runSummary(r, ud.Collected), s.Dim.Render(took), s.Dim.Render(fmt.Sprintf("%d runs logged", ud.Runs)))}
	steps := make([]string, 0, len(r.Steps))
	for _, st := range r.Steps {
		if st.Name == "summary" {
			continue
		}
		if len(st.Failed) > 0 {
			steps = append(steps, s.Warn.Render(s.Glyph.Warn+" "+st.Name))
		} else {
			steps = append(steps, s.OK.Render(s.Glyph.OK)+" "+st.Name)
		}
	}
	out = append(out, "  "+strings.Join(steps, "  "))
	for _, st := range r.Steps {
		for _, f := range st.Failed {
			out = append(out, "  "+s.Warn.Render(s.Glyph.Warn)+" "+st.Name+": "+s.Dim.Render(f))
		}
	}
	if r.RebootRequired {
		out = append(out, "  "+s.Warn.Render(s.Glyph.Warn+" run reported REBOOT REQUIRED"))
	}
	return out
}

func kv(k, v string) string { return theme.Current().Dim.Render(k+" ") + v }

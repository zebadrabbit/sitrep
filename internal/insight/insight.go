// Package insight turns collected module data into plain-English findings:
// the three lines on Overview that say whether the box needs a human, and
// the body of `sitrep why`. Pure functions over Data; nothing is collected
// here. Borrowed from syswatch's Insights tab (docs/DECISIONS.md).
package insight

import (
	"fmt"
	"strings"

	"github.com/zebadrabbit/sitrep/internal/modules/cron"
	"github.com/zebadrabbit/sitrep/internal/modules/disks"
	"github.com/zebadrabbit/sitrep/internal/modules/docker"
	"github.com/zebadrabbit/sitrep/internal/modules/logs"
	"github.com/zebadrabbit/sitrep/internal/modules/ports"
	"github.com/zebadrabbit/sitrep/internal/modules/services"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
	"github.com/zebadrabbit/sitrep/internal/modules/updates"
)

// Level is how loudly a finding should be shown. Crit is outage or data
// loss imminent; Warn is anything else a human should look at.
type Level int

const (
	Warn Level = iota
	Crit
)

// Insight is one finding. Tab names the module title to look at.
type Insight struct {
	Level Level  `json:"level"`
	Tab   string `json:"tab"`
	Text  string `json:"text"`
}

func (l Level) String() string {
	if l == Crit {
		return "crit"
	}
	return "warn"
}

func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// Thresholds. The disk ones match ui.Bar's ! and !!.
const (
	diskWarn, diskCrit = 85, 95
	memWarn, memCrit   = 90, 95
	swapCrit           = 50 // % of swap in use alongside memCrit
	loadPerCPU         = 2.0
	logsWarn           = 100 // errors in the last hour
)

// Check runs every rule over data keyed by module id (snapshot's shape).
// Missing or foreign values are skipped, so a partial store is fine.
// Crit findings come first; within a level, module registry order.
func Check(data map[string]any) []Insight {
	var out []Insight
	add := func(l Level, tab, format string, a ...any) {
		out = append(out, Insight{Level: l, Tab: tab, Text: fmt.Sprintf(format, a...)})
	}
	if d, ok := data["system"].(system.Data); ok {
		mem := pct(d.MemUsed, d.MemTotal)
		swap := pct(d.SwapUsed, d.SwapTotal)
		switch {
		case mem >= memCrit && swap >= swapCrit:
			add(Crit, "System", "memory %.0f%% used and swap %.0f%%: thrashing likely", mem, swap)
		case mem >= memWarn:
			add(Warn, "System", "memory %.0f%% used", mem)
		}
		if d.CPUs > 0 && d.Load[0] > loadPerCPU*float64(d.CPUs) {
			add(Warn, "System", "load %.1f on %d cpus", d.Load[0], d.CPUs)
		}
	}
	if d, ok := data["disks"].(disks.Data); ok {
		for _, m := range d.Mounts {
			switch {
			case m.Pct >= diskCrit:
				add(Crit, "Disks", "%s is %.0f%% full", m.Target, m.Pct)
			case m.Pct >= diskWarn:
				add(Warn, "Disks", "%s is %.0f%% full", m.Target, m.Pct)
			}
		}
		if d.SMART == "FAILED" {
			add(Crit, "Disks", "SMART reports a failing disk")
		}
	}
	if d, ok := data["services"].(services.Data); ok && d.Failed > 0 {
		var names []string
		for _, u := range d.Units {
			if u.Active == "failed" {
				names = append(names, strings.TrimSuffix(u.Unit, ".service"))
			}
		}
		add(Warn, "Services", "%s failed: %s", plural(d.Failed, "unit"), join(names))
	}
	if d, ok := data["cron"].(cron.Data); ok && d.Failed > 0 {
		var names []string
		for _, t := range d.Timers {
			if t.Result != "" && t.Result != "success" {
				names = append(names, strings.TrimSuffix(t.Unit, ".timer"))
			}
		}
		add(Warn, "Cron", "%s failed last run: %s", plural(d.Failed, "timer"), join(names))
	}
	if d, ok := data["docker"].(docker.Data); ok && d.Unhealthy > 0 {
		add(Warn, "Docker", "%s unhealthy", plural(d.Unhealthy, "container"))
	}
	if d, ok := data["updates"].(updates.Data); ok {
		switch {
		case d.RebootRequired && d.KernelStale:
			add(Warn, "Updates", "reboot required: running %s, installed %s", d.Kernel, d.NewestKernel)
		case d.RebootRequired || d.KernelStale:
			add(Warn, "Updates", "reboot required")
		}
		if d.Security > 0 {
			add(Warn, "Updates", "%s pending", plural(d.Security, "security update"))
		}
		if d.LastRun != nil && d.LastRun.Failures > 0 {
			add(Warn, "Updates", "last update run had %s", plural(d.LastRun.Failures, "failure"))
		}
	}
	if d, ok := data["logs"].(logs.Data); ok && d.LastHour >= logsWarn {
		if d.Top != "" {
			add(Warn, "Logs", "%s in the last hour, mostly %s", plural(d.LastHour, "error"), d.Top)
		} else {
			add(Warn, "Logs", "%s in the last hour", plural(d.LastHour, "error"))
		}
	}
	if d, ok := data["ports"].(ports.Data); ok && d.Unknown > 0 {
		add(Warn, "Ports", "%s unidentified", plural(d.Unknown, "listener"))
	}
	// Stable sort: crit first, otherwise the order above.
	var sorted []Insight
	for _, l := range []Level{Crit, Warn} {
		for _, i := range out {
			if i.Level == l {
				sorted = append(sorted, i)
			}
		}
	}
	return sorted
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// join lists up to three names, then "+n".
func join(names []string) string {
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" +%d", len(names)-3)
	}
	return strings.Join(names, ", ")
}

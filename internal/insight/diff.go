package insight

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zebadrabbit/sitrep/internal/modules/disks"
	"github.com/zebadrabbit/sitrep/internal/modules/docker"
	"github.com/zebadrabbit/sitrep/internal/modules/ports"
	"github.com/zebadrabbit/sitrep/internal/modules/services"
	"github.com/zebadrabbit/sitrep/internal/modules/sessions"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
	"github.com/zebadrabbit/sitrep/internal/modules/updates"
)

// Change is one difference between two snapshots. Kind is "new", "gone"
// or "changed"; the renderer maps it to + ○ !.
type Change struct {
	Kind string `json:"kind"`
	Tab  string `json:"tab"`
	Text string `json:"text"`
}

// Snapshot is what `sitrep snapshot` writes: module id → Data as JSON.
type Snapshot map[string]json.RawMessage

// diskStep is how many points a mount must move before it is reported.
const diskStep = 5

// Diff reports what changed from old to cur: listeners, containers, unit
// states, sessions, mounts, kernel, reboots, pending updates. Modules
// missing from either side are skipped, so a snapshot from a box with
// fewer modules still diffs cleanly. Borrowed from syswatch's `diff`.
func Diff(old, cur Snapshot) []Change {
	var out []Change
	add := func(kind, tab, format string, a ...any) {
		out = append(out, Change{Kind: kind, Tab: tab, Text: fmt.Sprintf(format, a...)})
	}
	if a, b, ok := both[system.Data](old, cur, "system"); ok {
		if a.Kernel != b.Kernel {
			add("changed", "System", "kernel %s → %s", a.Kernel, b.Kernel)
		}
		if b.Uptime < a.Uptime {
			add("changed", "System", "rebooted (uptime reset)")
		}
	}
	if a, b, ok := both[ports.Data](old, cur, "ports"); ok {
		keyed(a.Rows, b.Rows, portKey,
			func(r ports.Row) { add("new", "Ports", "%s", portText(r)) },
			func(r ports.Row) { add("gone", "Ports", "%s", portText(r)) },
			func(x, y ports.Row) {
				// Unprivileged snapshots have no process name; only a known → known change counts.
				if x.Process != "" && y.Process != "" && x.Process != y.Process {
					add("changed", "Ports", "%s :%d  %s → %s", y.Proto, y.Port, x.Process, y.Process)
				}
			})
	}
	if a, b, ok := both[docker.Data](old, cur, "docker"); ok {
		keyed(a.Containers, b.Containers, func(c docker.Container) string { return c.Name },
			func(c docker.Container) { add("new", "Docker", "%s  %s (%s)", c.Name, c.Image, c.State) },
			func(c docker.Container) { add("gone", "Docker", "%s  %s", c.Name, c.Image) },
			func(x, y docker.Container) {
				if x.State != y.State {
					add("changed", "Docker", "%s  %s → %s", y.Name, x.State, y.State)
				} else if x.Image != y.Image {
					add("changed", "Docker", "%s  image %s → %s", y.Name, x.Image, y.Image)
				}
			})
	}
	if a, b, ok := both[services.Data](old, cur, "services"); ok {
		keyed(a.Units, b.Units, func(u services.Unit) string { return u.Unit },
			func(u services.Unit) { add("new", "Services", "%s  %s", u.Unit, u.Active) },
			func(u services.Unit) { add("gone", "Services", "%s", u.Unit) },
			func(x, y services.Unit) {
				if x.Active != y.Active {
					add("changed", "Services", "%s  %s → %s", y.Unit, x.Active, y.Active)
				}
			})
	}
	if a, b, ok := both[sessions.Data](old, cur, "sessions"); ok {
		keyed(a.Sessions, b.Sessions, func(s sessions.Session) string { return s.User + "@" + s.Remote + " " + s.TTY },
			func(s sessions.Session) { add("new", "Sessions", "%s from %s on %s", s.User, s.Remote, s.TTY) },
			func(s sessions.Session) { add("gone", "Sessions", "%s from %s on %s", s.User, s.Remote, s.TTY) },
			nil)
	}
	if a, b, ok := both[disks.Data](old, cur, "disks"); ok {
		keyed(a.Mounts, b.Mounts, func(m disks.Mount) string { return m.Target },
			func(m disks.Mount) { add("new", "Disks", "%s mounted (%s)", m.Target, m.Source) },
			func(m disks.Mount) { add("gone", "Disks", "%s unmounted", m.Target) },
			func(x, y disks.Mount) {
				if d := y.Pct - x.Pct; d >= diskStep || d <= -diskStep {
					add("changed", "Disks", "%s  %.0f%% → %.0f%%", y.Target, x.Pct, y.Pct)
				}
			})
	}
	if a, b, ok := both[updates.Data](old, cur, "updates"); ok {
		if a.RebootRequired != b.RebootRequired {
			add("changed", "Updates", "reboot required: %t → %t", a.RebootRequired, b.RebootRequired)
		}
		if len(a.Packages) != len(b.Packages) {
			add("changed", "Updates", "%d → %d packages pending (%d → %d security)", len(a.Packages), len(b.Packages), a.Security, b.Security)
		}
	}
	return out
}

// portKey folds a port across addresses and families, the way the Ports
// tab groups rows. Not by process: unprivileged snapshots don't have one.
func portKey(r ports.Row) string {
	return fmt.Sprintf("%s/%d", strings.TrimSuffix(r.Proto, "6"), r.Port)
}

// portText is "tcp :22  ssh (sshd)", process only when known.
func portText(r ports.Row) string {
	t := fmt.Sprintf("%s :%d  %s", r.Proto, r.Port, r.Identity.Name)
	if r.Process != "" {
		t += " (" + r.Process + ")"
	}
	return t
}

// both decodes one module from each side; false when either is absent or
// is snapshot's {"error": ...} shape.
func both[T any](old, cur Snapshot, id string) (a, b T, ok bool) {
	ra, okA := old[id]
	rb, okB := cur[id]
	if !okA || !okB || json.Unmarshal(ra, &a) != nil || json.Unmarshal(rb, &b) != nil {
		return a, b, false
	}
	return a, b, true
}

// keyed walks two slices by key: added in cur, gone from old, and, when
// changed is non-nil, present in both.
func keyed[T any](old, cur []T, key func(T) string, added, gone func(T), changed func(x, y T)) {
	seen := map[string]T{}
	for _, x := range old {
		seen[key(x)] = x
	}
	now := map[string]bool{}
	for _, y := range cur {
		k := key(y)
		if now[k] {
			continue // second address of the same listener
		}
		now[k] = true
		x, ok := seen[k]
		switch {
		case !ok:
			added(y)
		case changed != nil:
			changed(x, y)
		}
	}
	done := map[string]bool{}
	for _, x := range old {
		k := key(x)
		if !now[k] && !done[k] {
			done[k] = true
			gone(x)
		}
	}
}

package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules/ports"
)

// Package hints for missing binaries (HANDOFF §8.5).
var packageHint = map[string]string{
	"ss": "iproute2", "smbstatus": "samba", "testparm": "samba", "exportfs": "nfs-kernel-server",
	"smartctl": "smartmontools", "docker": "docker.io", "lsblk": "util-linux", "journalctl": "systemd",
}

const slowCollect = 500 * time.Millisecond

// detection reports what the box looks like and what that disabled.
func detection(env detect.Env, entries []module.Entry) Section {
	cs := []Check{
		{OK, "init", env.Init},
	}
	if env.Init != "systemd" {
		cs[0] = Check{Degraded, "init", env.Init + " — services module unsupported"}
	}
	if env.Appliance != "" {
		cs = append(cs, Check{Degraded, "appliance", env.Appliance + " — appliance_sensitive modules need `sitrep modules enable`"})
	} else {
		cs = append(cs, Check{OK, "appliance", "none detected"})
	}
	rt := env.Runtime
	if rt == "" {
		rt = "none"
	}
	cs = append(cs, Check{OK, "container runtime", rt})
	var off []string
	for _, e := range entries {
		if !e.Enabled {
			off = append(off, e.Module.ID())
		}
	}
	if len(off) > 0 {
		cs = append(cs, Check{Degraded, "disabled", strings.Join(off, ", ")})
	}
	return Section{"Detection", cs}
}

// modulesSection runs Detect and a timed dry-run Collect per module.
func modulesSection(ctx context.Context, entries []module.Entry) (Section, map[string]module.Data) {
	var cs []Check
	data := map[string]module.Data{}
	for _, e := range entries {
		m := e.Module
		if m.Interval() == 0 {
			continue
		}
		label := m.ID()
		if !e.Enabled {
			cs = append(cs, Check{Degraded, label, fmt.Sprintf("%s: %s%s", e.Avail.State, e.Avail.Reason, hint(e.Avail.Reason))})
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, module.Timeout(m))
		start := time.Now()
		d, err := m.Collect(cctx)
		took := time.Since(start).Round(time.Millisecond)
		cancel()
		switch {
		case err != nil:
			cs = append(cs, Check{Fail, label, err.Error()})
		case took > slowCollect:
			cs = append(cs, Check{Degraded, label, fmt.Sprintf("%s — slow; consider raising its interval (%s)", took, e.Avail.Reason)})
		default:
			state := OK
			if e.Avail.State != module.Available {
				state = Degraded
			}
			cs = append(cs, Check{state, label, fmt.Sprintf("%s · %s", took, e.Avail.Reason)})
		}
		data[m.ID()] = d
	}
	return Section{"Modules", cs}, data
}

// hint appends "(apt install pkg)" when a known binary is named as missing.
func hint(reason string) string {
	for bin, pkg := range packageHint {
		if strings.Contains(reason, bin+" missing") {
			return fmt.Sprintf(" — package: %s", pkg)
		}
	}
	return ""
}

// portsSection counts identity sources from the dry-run data.
func portsSection(d module.Data) Section {
	pd, ok := d.(ports.Data)
	if !ok {
		return Section{"Ports table", []Check{{Degraded, "identities", "no data (module disabled or failed)"}}}
	}
	srcs := make([]string, 0, len(pd.Sources))
	for s := range pd.Sources {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	parts := make([]string, 0, len(srcs))
	for _, s := range srcs {
		parts = append(parts, fmt.Sprintf("%s %d", s, pd.Sources[s]))
	}
	cs := []Check{{OK, "listeners", fmt.Sprintf("%d (%d established)", pd.Listening, pd.Established)}, {OK, "identity sources", strings.Join(parts, ", ")}}
	if pd.Unknown == 0 {
		cs = append(cs, Check{OK, "unidentified", "0"})
	} else {
		cs = append(cs, Check{Degraded, "unidentified", fmt.Sprintf("%d — add them to %s/ports.toml", pd.Unknown, config.Dir())})
	}
	if pd.Fallback {
		cs = append(cs, Check{Degraded, "source", "/proc/net fallback (ss missing)"})
	}
	return Section{"Ports table", cs}
}

// coldStart execs ourselves with --once --demo and times the whole thing:
// process start to a full frame on stdout. This is the honest number
// (HANDOFF §10.8: < 150 ms).
func coldStart() Check {
	exe, err := os.Executable()
	if err != nil {
		return Check{Degraded, "cold start", "cannot locate own binary"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	out, err := exec.CommandContext(ctx, exe, "--once", "--demo", "--size", "100x30").Output()
	took := time.Since(start).Round(time.Millisecond)
	switch {
	case err != nil:
		return Check{Fail, "cold start", "--once --demo failed: " + err.Error()}
	case len(out) == 0:
		return Check{Fail, "cold start", "--once --demo printed nothing"}
	case took > 150*time.Millisecond:
		return Check{Degraded, "cold start", fmt.Sprintf("%s to first frame (target < 150ms)", took)}
	}
	return Check{OK, "cold start", fmt.Sprintf("%s to first frame", took)}
}

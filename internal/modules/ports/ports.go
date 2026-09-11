// Package ports is the hero module: what is listening, how long, and what
// it probably is (HANDOFF §6).
package ports

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
)

const newWindow = 60 * time.Second

// Row is one listener, enriched.
type Row struct {
	Proto    string        `json:"proto"`
	Addr     string        `json:"addr"`
	Port     int           `json:"port"`
	PID      int           `json:"pid"`
	PPID     int           `json:"ppid,omitempty"`
	Process  string        `json:"process"`
	User     string        `json:"user"`
	Since    time.Time     `json:"since"`
	Age      time.Duration `json:"age"`
	Conn     int           `json:"conn"`
	Identity Identity      `json:"identity"`
	Loopback bool          `json:"loopback"`
	New      bool          `json:"new"`
	Gone     bool          `json:"gone,omitempty"`
	Detail   Detail        `json:"detail"`
	Peers    []Peer        `json:"peers,omitempty"`
}

// Detail is the per-process data shown on enter.
type Detail struct {
	Cmdline string `json:"cmdline,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Exe     string `json:"exe,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Threads int    `json:"threads,omitempty"`
	FDs     int    `json:"fds"`
	// Source is the file actually running: the exe, or the script when the
	// exe is an interpreter. Modified after Since means the process is stale.
	Source    string    `json:"source,omitempty"`
	SourceMod time.Time `json:"source_mod,omitempty"`
	Stale     bool      `json:"stale,omitempty"`
	CPU       float64   `json:"cpu"`
	RSS       uint64    `json:"rss"`
}

// Peer is an established connection to a listener.
type Peer struct {
	Remote string `json:"remote"`
	State  string `json:"state"`
}

// Data is one collection.
type Data struct {
	Rows        []Row          `json:"rows"`
	Listening   int            `json:"listening"`
	Established int            `json:"established"`
	Newest      string         `json:"newest,omitempty"`
	Sources     map[string]int `json:"sources"`
	Unknown     int            `json:"unknown"`
	Fallback    bool           `json:"fallback"` // /proc/net used because ss is missing
	Collected   time.Time      `json:"collected"`
}

// Module state that persists across collections: first-seen times for the
// `+` marker, the previous row set for strikethrough, and UI state.
type Module struct {
	run     *collect.Runner
	demo    bool
	res     *resolver
	started time.Time
	seen    map[string]time.Time
	prev    map[string]Row
	ui      uiState
}

// New builds the module. The resolver loads lazily on first Collect so a
// broken user ports.toml surfaces as a collection error, not a crash.
func New(demo bool) *Module {
	return &Module{run: collect.New("ports", demo), demo: demo, started: time.Now(), seen: map[string]time.Time{}}
}

func (*Module) ID() string              { return "ports" }
func (*Module) Title() string           { return "Ports" }
func (*Module) Flags() module.Flags     { return module.Flags{NeedsRootForFull: true} }
func (*Module) Interval() time.Duration { return 3 * time.Second }

func (*Module) Info() string {
	return `Collects: every listening TCP/UDP socket with its owning process, user, AGE,
established connection count, and a best-guess IDENTITY.
AGE is the owning process's start time from /proc/<pid>/stat — the kernel does
not expose socket age, so this is the honest proxy. ◐ when the pid is not
visible (needs root or cap_sys_ptrace).
CONN counts established connections to that local port from ss -tunaH; UDP shows —.
IDENTITY resolution, first hit wins: docker port map (●, Phase 2), process
name table (●), curated port table ports.toml (◐, override in
~/.config/sitrep/ports.toml), /etc/services (◐), heuristic (○).
Execs:    ss -tulnpH, ss -tunaH. Falls back to /proc/net/{tcp,udp}{,6} + a
/proc/*/fd scan when ss is missing.
Rows are grouped by process and port across addresses and IPv4/IPv6 (nmbd on
16 addresses is one row). space expands a group in place; c shows the flat list.
Probe (p in detail): the ONLY outbound network activity sitrep performs, only
on keypress, only to 127.0.0.1:<port>. HEAD / for http-ish identities,
otherwise the first 128 bytes of banner, 1s timeout.
Interval: 3s.`
}

func (m *Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		return module.Availability{State: module.Missing, Reason: "/proc/net/tcp not readable"}
	}
	switch {
	case !detect.Has("ss"):
		return module.Availability{State: module.Degraded, Reason: "ss missing (iproute2); using /proc/net fallback"}
	case !env.Root:
		return module.Availability{State: module.NeedsRoot, Reason: "unprivileged: other users' pids show as ◐"}
	}
	return module.Availability{State: module.Available, Reason: "ss + /proc"}
}

// Collect gathers listeners and established sockets, enriches from /proc,
// resolves identities, and diffs against the previous set.
func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	if m.res == nil {
		svc, _ := m.run.ReadFile("/etc/services")
		r, err := newResolver(config.Dir(), svc)
		if err != nil {
			return nil, err
		}
		m.res = r
	}
	ls, es, fallback, err := m.sockets(ctx)
	if err != nil {
		return nil, err
	}
	now := m.now()
	boot, _ := m.run.BootTime()
	counts := countEstablished(es)
	peers := peersByPort(es)
	d := Data{Sources: map[string]int{}, Fallback: fallback, Collected: now}
	cur := map[string]Row{}
	for _, l := range ls {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r := m.row(ctx, l, boot, now)
		r.Conn = counts[l.Proto+":"+strconv.Itoa(l.Port)]
		r.Peers = peers[l.Proto+":"+strconv.Itoa(l.Port)]
		d.Sources[r.Identity.Source]++
		if r.Identity.Source == SrcUnknown || r.Identity.Source == SrcHeuristic {
			d.Unknown++
		}
		cur[rowKey(r)] = r
		d.Rows = append(d.Rows, r)
	}
	d.Listening = len(d.Rows)
	for _, c := range counts {
		d.Established += c
	}
	m.diff(&d, cur, now)
	sort.Slice(d.Rows, func(i, j int) bool { return less(d.Rows[i], d.Rows[j], sortPort) })
	return d, nil
}

// now is wall time live; in demo it is the fixture's boot+uptime so ages
// are stable in screenshots.
func (m *Module) now() time.Time {
	if !m.demo {
		return time.Now()
	}
	boot, err := m.run.BootTime()
	if err != nil {
		return time.Now()
	}
	up, err := m.run.ReadFile("/proc/uptime")
	if err != nil {
		return time.Now()
	}
	secs, _ := strconv.ParseFloat(strings.Fields(string(up))[0], 64)
	return boot.Add(time.Duration(secs * float64(time.Second)))
}

// sockets tries ss, then /proc/net.
func (m *Module) sockets(ctx context.Context) ([]Listener, []Established, bool, error) {
	lres, err := m.run.Run(ctx, "ss", "-tulnpH")
	if err != nil && !errors.Is(err, collect.ErrMissing) && !errors.Is(err, collect.ErrNoFixture) {
		return nil, nil, false, err
	}
	if err == nil {
		eres, err := m.run.Run(ctx, "ss", "-tunaH")
		if err != nil {
			return nil, nil, false, err
		}
		return ParseListeners(lres.Stdout), ParseEstablished(eres.Stdout), false, nil
	}
	ls, es, err := m.procNet()
	return ls, es, true, err
}

func (m *Module) procNet() ([]Listener, []Established, error) {
	owners := m.socketOwners()
	var ls []Listener
	var es []Established
	any := false
	for _, f := range []struct {
		path, proto string
		v6          bool
	}{{"/proc/net/tcp", "tcp", false}, {"/proc/net/tcp6", "tcp6", true}, {"/proc/net/udp", "udp", false}, {"/proc/net/udp6", "udp6", true}} {
		b, err := m.run.ReadFile(f.path)
		if err != nil {
			continue
		}
		any = true
		rows := ParseProcNet(b, f.v6)
		ls = append(ls, listenersFromProcNet(rows, f.proto, owners)...)
		if strings.HasPrefix(f.proto, "tcp") {
			es = append(es, establishedFromProcNet(rows, f.proto)...)
		}
	}
	if !any {
		return nil, nil, errors.New("ports: neither ss nor /proc/net/tcp available")
	}
	return ls, es, nil
}

func (m *Module) row(ctx context.Context, l Listener, boot, now time.Time) Row {
	r := Row{Proto: l.Proto, Addr: l.Addr, Port: l.Port, PID: l.PID, Process: l.Process,
		Loopback: IsLoopback(l.Addr), Detail: Detail{FDs: -1}}
	if l.PID != 0 {
		l.Cmdline = m.run.ProcCmdline(l.PID)
		r.Detail.Cmdline = l.Cmdline
	}
	r.Identity = m.res.Resolve(l)
	if l.PID == 0 {
		return r
	}
	if st, err := m.run.ProcStart(l.PID, boot); err == nil {
		r.Since, r.Age = st, now.Sub(st)
	}
	if uid, err := m.run.ProcUID(l.PID); err == nil {
		r.User = collect.Username(uid)
	}
	r.Detail.Unit = m.run.ProcCgroup(l.PID)
	r.Detail.Cwd, _ = m.run.Readlink(fmt.Sprintf("/proc/%d/cwd", l.PID))
	r.Detail.Exe, _ = m.run.Readlink(fmt.Sprintf("/proc/%d/exe", l.PID))
	r.PPID, r.Detail.Threads = m.procStatus(l.PID)
	m.sourceAge(&r)
	if !m.demo {
		m.liveStats(ctx, &r)
	}
	return r
}

// sourceAge finds what is really running and when it last changed. An exe
// link ending in " (deleted)" means the binary was replaced underneath the
// process: stale, no mtime needed.
func (m *Module) sourceAge(r *Row) {
	d := &r.Detail
	if strings.HasSuffix(d.Exe, " (deleted)") {
		d.Source, d.Stale = strings.TrimSuffix(d.Exe, " (deleted)"), true
		return
	}
	d.Source = d.Exe
	if isInterpreter(d.Exe) {
		if script := scriptPath(d.Cmdline, d.Cwd); script != "" {
			d.Source = script
		}
	}
	if d.Source == "" {
		return
	}
	// Stat through the process's own root so container paths resolve.
	mod, err := m.run.ModTime(fmt.Sprintf("/proc/%d/root%s", r.PID, d.Source))
	if err != nil {
		return
	}
	d.SourceMod = mod
	d.Stale = !r.Since.IsZero() && mod.After(r.Since)
}

var interpreters = []string{"python", "perl", "node", "ruby", "php", "bash", "sh", "bun", "deno", "lua"}

// isInterpreter matches exe basenames like python3.12, perl, node.
func isInterpreter(exe string) bool {
	base := filepath.Base(exe)
	for _, i := range interpreters {
		if strings.HasPrefix(base, i) {
			return true
		}
	}
	return false
}

var scriptExt = map[string]bool{".py": true, ".js": true, ".mjs": true, ".ts": true, ".rb": true, ".php": true, ".pl": true, ".sh": true}

// scriptPath picks the first cmdline arg that looks like a script, resolved
// against cwd. "-m module" style invocations have no file; the exe stands.
func scriptPath(cmdline, cwd string) string {
	for _, a := range strings.Fields(cmdline)[min(1, len(strings.Fields(cmdline))):] {
		if strings.HasPrefix(a, "-") || !scriptExt[strings.ToLower(filepath.Ext(a))] {
			continue
		}
		if !filepath.IsAbs(a) && cwd != "" {
			a = filepath.Join(cwd, a)
		}
		return a
	}
	return ""
}

// procStatus pulls PPid and Threads from /proc/<pid>/status.
func (m *Module) procStatus(pid int) (ppid, threads int) {
	b, err := m.run.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PPid:"); ok {
			ppid, _ = strconv.Atoi(strings.TrimSpace(v))
		} else if v, ok := strings.CutPrefix(line, "Threads:"); ok {
			threads, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return ppid, threads
}

// liveStats adds CPU%, RSS and fd count from gopsutil; unreadable = left zero/-1.
func (m *Module) liveStats(ctx context.Context, r *Row) {
	p, err := process.NewProcessWithContext(ctx, int32(r.PID))
	if err != nil {
		return
	}
	if c, err := p.CPUPercentWithContext(ctx); err == nil {
		r.Detail.CPU = c
	}
	if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
		r.Detail.RSS = mi.RSS
	}
	if ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", r.PID)); err == nil {
		r.Detail.FDs = len(ents)
	}
}

// diff marks new rows (first seen after launch, within 60s) and appends
// rows that vanished since last time, struck through for one refresh.
func (m *Module) diff(d *Data, cur map[string]Row, now time.Time) {
	var newest *Row
	for i := range d.Rows {
		k := rowKey(d.Rows[i])
		first, ok := m.seen[k]
		if !ok {
			first = now
			if m.prev == nil {
				first = m.started // first collection: everything pre-dates launch
			}
			m.seen[k] = first
		}
		d.Rows[i].New = first.After(m.started) && now.Sub(first) < newWindow
		if newest == nil || d.Rows[i].Since.After(newest.Since) {
			newest = &d.Rows[i]
		}
	}
	if newest != nil && !newest.Since.IsZero() {
		d.Newest = fmt.Sprintf("%s :%d (%s)", newest.Identity.Name, newest.Port, ageStr(newest.Age))
	}
	for k, r := range m.prev {
		if _, ok := cur[k]; !ok {
			r.Gone = true
			d.Rows = append(d.Rows, r)
			delete(m.seen, k)
		}
	}
	m.prev = cur
}

func rowKey(r Row) string { return r.Proto + "/" + r.Addr + ":" + strconv.Itoa(r.Port) }

func countEstablished(es []Established) map[string]int {
	c := map[string]int{}
	for _, e := range es {
		if e.State == "ESTAB" {
			c[e.Proto+":"+strconv.Itoa(e.LocalPort)]++
		}
	}
	return c
}

func peersByPort(es []Established) map[string][]Peer {
	p := map[string][]Peer{}
	for _, e := range es {
		k := e.Proto + ":" + strconv.Itoa(e.LocalPort)
		if len(p[k]) < 10 {
			p[k] = append(p[k], Peer{Remote: e.Remote, State: e.State})
		}
	}
	return p
}

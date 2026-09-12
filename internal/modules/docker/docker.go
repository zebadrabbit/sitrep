// Package docker lists containers and feeds the Ports module's docker▸
// resolution (HANDOFF §5 #7, §6 layer 1). Podman is used when docker is
// absent; the CLI shape is the same.
package docker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Data is one collection.
type Data struct {
	Runtime    string      `json:"runtime"`
	Containers []Container `json:"containers"`
	Running    int         `json:"running"`
	Exited     int         `json:"exited"`
	Unhealthy  int         `json:"unhealthy"`
}

type Module struct {
	run  *collect.Runner
	demo bool
	bin  string
	mu   sync.RWMutex
	last Data // for Lookup from the ports collector goroutine
	// cgroup cpu usage_usec per container at the previous tick, for CPU%.
	prevCPU map[string]uint64
	statsAt time.Time
}

func New(demo bool) *Module {
	return &Module{run: collect.New("docker", demo), demo: demo, bin: "docker"}
}

func (*Module) ID() string              { return "docker" }
func (*Module) Title() string           { return "Docker" }
func (*Module) Flags() module.Flags     { return module.Flags{} }
func (*Module) Interval() time.Duration { return 5 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: every container (running or not) with image, status, health,
uptime, published ports and compose project; CPU% and memory (against the
limit when one is set) per running container from cgroup v2, no docker stats
exec. Feeds Ports: a listener owned by
docker-proxy resolves to docker▸<container>, and a process inside a
host-network container is matched through its cgroup.
Needs:    docker CLI and socket access (docker group or root). Podman is used
when docker is absent.
Execs:    docker ps -a --format '{{json .}}'  (no per-container inspect: one exec per cycle)
Reads:    /sys/fs/cgroup/<container scope>/{cpu.stat,memory.current,memory.max}
Interval: 5s.`
}

func (m *Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	switch env.Runtime {
	case "docker":
		m.bin = "docker"
	case "podman":
		m.bin = "podman"
	default:
		return module.Availability{State: module.Missing, Reason: "docker missing (or podman)"}
	}
	return module.Availability{State: module.Available, Reason: m.bin + " cli"}
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, m.bin, "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		if errors.Is(err, collect.ErrMissing) && m.bin == "docker" && detect.Has("podman") {
			m.bin = "podman"
			return m.Collect(ctx)
		}
		if strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("docker: socket permission denied — add yourself to the docker group or run with sudo")
		}
		return nil, err
	}
	d := Data{Runtime: m.bin, Containers: ParsePS(res.Stdout)}
	m.stats(d.Containers, time.Now())
	for _, c := range d.Containers {
		switch {
		case c.Running && c.Health == "unhealthy":
			d.Unhealthy++
			d.Running++
		case c.Running:
			d.Running++
		default:
			d.Exited++
		}
	}
	// Unhealthy first, then running, then exited (old compose leftovers are
	// noise, not news), each by name.
	sort.SliceStable(d.Containers, func(i, j int) bool {
		a, b := d.Containers[i], d.Containers[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.Name < b.Name
	})
	m.mu.Lock()
	m.last = d
	m.mu.Unlock()
	return d, nil
}

func rank(c Container) int {
	switch {
	case c.Health == "unhealthy":
		return 0
	case c.Running:
		return 1
	}
	return 2
}

// Ref is what Ports needs to name a listener.
type Ref struct {
	Name, Image string
}

// LookupPort maps a published host port to its container.
func (m *Module) LookupPort(proto string, port int) (Ref, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proto = strings.TrimSuffix(proto, "6")
	for _, c := range m.last.Containers {
		for _, p := range c.Ports {
			if p.HostPort == port && p.Proto == proto {
				return Ref{Name: c.Name, Image: c.Image}, true
			}
		}
	}
	return Ref{}, false
}

// LookupID maps a container id (or prefix, from a cgroup path) to its container.
func (m *Module) LookupID(id string) (Ref, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.last.Containers {
		if strings.HasPrefix(id, c.ID) || strings.HasPrefix(c.ID, id) {
			return Ref{Name: c.Name, Image: c.Image}, true
		}
	}
	return Ref{}, false
}

func (*Module) Card(d module.Data, w int) string {
	dd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	line := fmt.Sprintf("%s running", s.Bold.Render(fmt.Sprint(dd.Running)))
	if dd.Unhealthy > 0 {
		line += "  " + s.Crit.Render(fmt.Sprintf("%s %d unhealthy", s.Glyph.Crit, dd.Unhealthy))
	}
	if dd.Exited > 0 {
		line += "  " + s.Dim.Render(fmt.Sprintf("%s %d exited", s.Glyph.Fail, dd.Exited))
	}
	lines := []string{line}
	if top := topCPU(dd.Containers); top != nil {
		lines = append(lines, s.Dim.Render("top ")+" "+top.Name+"  "+cpuStr(*top)+"  "+memStr(*top))
	}
	for _, c := range dd.Containers {
		if c.Health == "unhealthy" || c.Health == "starting" {
			lines = append(lines, s.Warn.Render(s.Glyph.Warn)+" "+c.Name+"  "+s.Dim.Render(c.Status))
		}
		if len(lines) == 4 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	dd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	rows := make([][]string, 0, len(dd.Containers))
	for _, c := range dd.Containers {
		g := s.OK.Render(s.Glyph.OK)
		switch {
		case c.Health == "unhealthy":
			g = s.Crit.Render(s.Glyph.Crit)
		case !c.Running:
			g = s.Dim.Render(s.Glyph.Fail)
		case c.Health == "starting":
			g = s.Warn.Render(s.Glyph.Degraded)
		}
		rows = append(rows, []string{g + " " + c.Name, cpuStr(c), memStr(c), ShortImage(c.Image) + s.Dim.Render(tag(c.Image)), c.Status, ports(c.Ports), s.Dim.Render(c.Project)})
	}
	out := ui.Table([]string{"  NAME", "CPU", "MEM", "IMAGE", "STATUS", "PORTS", "PROJECT"}, rows, w)
	out += "\n" + s.Dim.Render(fmt.Sprintf("%d containers via %s", len(dd.Containers), dd.Runtime))
	if h > 0 {
		out = strings.Join(strings.Split(out, "\n")[:min(h, strings.Count(out, "\n")+1)], "\n")
	}
	return out
}

func tag(img string) string {
	if i := strings.LastIndexAny(img, ":@"); i >= 0 && !strings.Contains(img[i:], "/") {
		return img[i:]
	}
	return ""
}

// ports renders "3000→1337 8446→8443/udp", deduplicating the v4/v6 pair.
func ports(ps []PortMap) string {
	seen := map[string]bool{}
	var parts []string
	for _, p := range ps {
		k := fmt.Sprintf("%d→%d", p.HostPort, p.ContainerPort)
		if p.Proto != "tcp" {
			k += "/" + p.Proto
		}
		if !seen[k] {
			seen[k] = true
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, " ")
}

func cpuStr(c Container) string {
	if !c.HasCPU {
		return ""
	}
	txt := fmt.Sprintf("%.1f%%", c.CPUPct)
	if c.CPUPct >= 100 {
		return theme.Current().Warn.Render(txt) // more than a core, sustained: worth a look
	}
	return txt
}

func memStr(c Container) string {
	if !c.HasMem {
		return ""
	}
	txt := ui.Bytes(c.Mem)
	if c.MemLimit > 0 {
		p := float64(c.Mem) / float64(c.MemLimit) * 100
		txt += fmt.Sprintf(" %.0f%%", p)
		if p >= 90 {
			return theme.Current().Crit.Render(txt) // about to be OOM-killed
		}
	}
	return txt
}

func topCPU(cs []Container) *Container {
	var top *Container
	for i := range cs {
		if cs[i].HasCPU && (top == nil || cs[i].CPUPct > top.CPUPct) {
			top = &cs[i]
		}
	}
	return top
}

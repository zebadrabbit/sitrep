package system

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"

	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Topology is what lscpu knows: static, collected once. Empty when lscpu is
// missing; the view then shows only the cpu count.
type Topology struct {
	Model   string  `json:"model"`
	Sockets int     `json:"sockets"`
	Cores   int     `json:"cores"`   // per socket
	Threads int     `json:"threads"` // per core
	MinMHz  float64 `json:"min_mhz"`
	MaxMHz  float64 `json:"max_mhz"`
	Nodes   []Node  `json:"nodes"`
}

// Node is one NUMA node. Memory is refreshed every tick from sysfs.
type Node struct {
	ID       int    `json:"id"`
	List     string `json:"list"` // lscpu's "0-3,8-11" form, for display
	CPUs     []int  `json:"cpus"`
	MemTotal uint64 `json:"mem_total"`
	MemUsed  uint64 `json:"mem_used"`
}

// ParseLscpu reads `lscpu -J`. Fields vary by arch and version, so every
// one is optional; only a non-lscpu document is an error.
func ParseLscpu(b []byte) (Topology, error) {
	var doc struct {
		Lscpu []struct{ Field, Data string } `json:"lscpu"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Topology{}, fmt.Errorf("system: parse lscpu: %w", err)
	}
	var t Topology
	for _, f := range doc.Lscpu {
		atoi := func() int { n, _ := strconv.Atoi(f.Data); return n }
		atof := func() float64 { x, _ := strconv.ParseFloat(f.Data, 64); return x }
		switch f.Field {
		case "Model name:":
			t.Model = cleanModel(f.Data)
		case "Socket(s):":
			t.Sockets = atoi()
		case "Core(s) per socket:":
			t.Cores = atoi()
		case "Thread(s) per core:":
			t.Threads = atoi()
		case "CPU min MHz:":
			t.MinMHz = atof()
		case "CPU max MHz:":
			t.MaxMHz = atof()
		default:
			// "NUMA node3 CPU(s):"
			if rest, ok := strings.CutPrefix(f.Field, "NUMA node"); ok && strings.HasSuffix(rest, " CPU(s):") {
				id, err := strconv.Atoi(strings.TrimSuffix(rest, " CPU(s):"))
				if err == nil && f.Data != "" {
					t.Nodes = append(t.Nodes, Node{ID: id, List: f.Data, CPUs: parseCPUList(f.Data)})
				}
			}
		}
	}
	return t, nil
}

// cleanModel drops the marketing noise so the line fits: "Intel(R) Xeon(R) CPU
// E5504 @ 2.00GHz" becomes "Intel Xeon E5504 @ 2.00GHz".
func cleanModel(s string) string {
	s = strings.NewReplacer("(R)", "", "(TM)", "", "(tm)", "").Replace(s)
	var out []string
	for _, w := range strings.Fields(s) {
		if w != "CPU" && w != "Processor" {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// parseCPUList expands the kernel's "0-3,8,10-11" form.
func parseCPUList(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		lo, hi, ok := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			continue
		}
		b := a
		if ok {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				continue
			}
		}
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
	}
	return out
}

// ParseNodeMeminfo reads /sys/devices/system/node/nodeN/meminfo
// ("Node 0 MemTotal:  37080724 kB") for total and used bytes.
func ParseNodeMeminfo(b []byte) (total, used uint64) {
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		kb, _ := strconv.ParseUint(f[3], 10, 64)
		switch f[2] {
		case "MemTotal:":
			total = kb * 1024
		case "MemUsed:":
			used = kb * 1024
		}
	}
	return total, used
}

// topology runs lscpu once and caches it; node memory is re-read each call.
func (m *Module) topology(ctx context.Context) Topology {
	if m.topo == nil {
		var t Topology
		if res, err := m.run.Run(ctx, "lscpu", "-J"); err == nil {
			t, _ = ParseLscpu(res.Stdout)
		}
		m.topo = &t
	}
	t := *m.topo
	t.Nodes = append([]Node(nil), t.Nodes...)
	for i := range t.Nodes {
		b, _ := m.run.ReadFile(fmt.Sprintf("/sys/devices/system/node/node%d/meminfo", t.Nodes[i].ID))
		t.Nodes[i].MemTotal, t.Nodes[i].MemUsed = ParseNodeMeminfo(b)
	}
	return t
}

func perCPU(ctx context.Context) []float64 {
	p, err := cpu.PercentWithContext(ctx, 0, true)
	if err != nil {
		return nil
	}
	return p
}

// cpuLine is "cpu   2× Intel Xeon E5504 @ 2.00GHz   4 cores × 1 thread each   1.6–2.0 GHz".
func cpuLine(sd Data) string {
	s := theme.Current()
	t := sd.Topo
	if t.Model == "" {
		return fmt.Sprintf("%s  %d cpus", s.Dim.Render("cpu "), sd.CPUs)
	}
	parts := []string{fmt.Sprintf("%d× %s", max(1, t.Sockets), t.Model)}
	if t.Cores > 0 && t.Threads > 0 {
		parts = append(parts, fmt.Sprintf("%d cores × %d thread each", t.Cores, t.Threads))
	}
	if t.MaxMHz > 0 {
		parts = append(parts, fmt.Sprintf("%.1f–%.1f GHz", t.MinMHz/1000, t.MaxMHz/1000))
	}
	return s.Dim.Render("cpu ") + "  " + strings.Join(parts, "   ")
}

// cpuBlock is the htop-style per-cpu grid grouped by NUMA node, falling back
// to one cell per cpu (a sparkline read left to right) when the grid would
// not fit in h lines: a quad-socket EPYC has more cpus than a screen has rows.
func cpuBlock(sd Data, w, h int) string {
	nodes := sd.Topo.Nodes
	if len(nodes) == 0 {
		all := make([]int, len(sd.PerCPU))
		for i := range all {
			all[i] = i
		}
		nodes = []Node{{ID: -1, CPUs: all}}
	}
	const cellW = 20 // "  0 ██████████ 40%" + gap
	perRow := max(1, w/cellW)
	rows := 0
	for _, n := range nodes {
		rows += 1 + (len(n.CPUs)+perRow-1)/perRow
	}
	if rows > h {
		return cpuStrip(sd, nodes)
	}
	s := theme.Current()
	var out []string
	for _, n := range nodes {
		if n.ID >= 0 {
			out = append(out, nodeHeader(n))
		}
		cells := make([]string, 0, len(n.CPUs))
		for _, c := range n.CPUs {
			v := sd.pct(c)
			cells = append(cells, fmt.Sprintf("%s %s %3.0f%%", s.Dim.Render(fmt.Sprintf("%3d", c)), ui.Bar(v, 10), v))
		}
		for i := 0; i < len(cells); i += perRow {
			out = append(out, strings.Join(cells[i:min(i+perRow, len(cells))], " "))
		}
	}
	return strings.Join(out, "\n")
}

// cpuStrip is one line per node: header, then one sparkline cell per cpu.
func cpuStrip(sd Data, nodes []Node) string {
	s := theme.Current()
	var out []string
	for _, n := range nodes {
		vals := make([]float64, len(n.CPUs))
		for i, c := range n.CPUs {
			vals[i] = sd.pct(c)
		}
		label := s.Dim.Render(fmt.Sprintf("%-6s", "cpus"))
		if n.ID >= 0 {
			label = s.Dim.Render(fmt.Sprintf("node%-2d", n.ID)) + " " + ui.Bytes(n.MemUsed) + "/" + ui.Bytes(n.MemTotal) + " "
		}
		out = append(out, label+" "+ui.Sparkline(vals, len(vals), 100))
	}
	return strings.Join(out, "\n")
}

func nodeHeader(n Node) string {
	s := theme.Current()
	return fmt.Sprintf("%s  cpus %s  mem %s / %s", s.Bold.Render(fmt.Sprintf("node%d", n.ID)), n.List, ui.Bytes(n.MemUsed), ui.Bytes(n.MemTotal))
}

// pct is one cpu's last-tick utilisation, 0 when the sample is short.
func (sd Data) pct(c int) float64 {
	if c < 0 || c >= len(sd.PerCPU) {
		return 0
	}
	return sd.PerCPU[c]
}

// Package system is the small host summary: load, CPU sparkline, memory,
// top processes. btop does the rest (HANDOFF §5 #3).
package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"

	sitrep "github.com/zebadrabbit/sitrep"
	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

const histLen = 60

// Proc is one row of the top-5 tables.
type Proc struct {
	PID  int32   `json:"pid"`
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"`
	RSS  uint64  `json:"rss"`
}

// Data is the collected snapshot. JSON tags feed `sitrep snapshot`.
type Data struct {
	Hostname string        `json:"hostname"`
	Distro   string        `json:"distro"`
	Kernel   string        `json:"kernel"`
	Arch     string        `json:"arch"`
	Uptime   time.Duration `json:"uptime"`
	Load     [3]float64    `json:"load"`
	CPUs     int           `json:"cpus"`
	// Pressure is PSI "some avg10" for cpu, memory, io: the share of the last
	// 10s some task spent stalled on that resource. 0 when the kernel has no PSI.
	Pressure  [3]float64 `json:"pressure"`
	CPUPct    float64    `json:"cpu_pct"`
	PerCPU    []float64  `json:"per_cpu"`
	Topo      Topology   `json:"topology"`
	CPUHist   []float64  `json:"cpu_hist"`
	MemUsed   uint64     `json:"mem_used"`
	MemTotal  uint64     `json:"mem_total"`
	SwapUsed  uint64     `json:"swap_used"`
	SwapTotal uint64     `json:"swap_total"`
	TopCPU    []Proc     `json:"top_cpu"`
	TopRSS    []Proc     `json:"top_rss"`
}

type Module struct {
	demo bool
	hist []float64
	run  *collect.Runner
	topo *Topology // lscpu result, cached after the first tick
}

func New(demo bool) *Module { return &Module{demo: demo, run: collect.New("system", demo)} }

func (*Module) ID() string              { return "system" }
func (*Module) Title() string           { return "System" }
func (*Module) Flags() module.Flags     { return module.Flags{} }
func (*Module) Interval() time.Duration { return 2 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: distro, kernel, arch, uptime, load 1/5/15, CPU% (60-sample ring for the
sparkline), per-cpu utilisation, CPU model and socket/core/thread topology, NUMA
nodes with their cpus and memory, memory and swap, PSI pressure (some avg10 for
cpu, memory, io), top 5 processes by CPU and by RSS.
Needs:    /proc. No root needed. Without lscpu the topology lines are omitted.
Execs:    lscpu -J, once; everything else is gopsutil and sysfs reads.
Interval: 2s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if _, err := os.Stat("/proc/stat"); err != nil {
		return module.Availability{State: module.Missing, Reason: "/proc not readable"}
	}
	return module.Availability{State: module.Available, Reason: "/proc"}
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	if m.demo {
		return m.demoData()
	}
	d, err := gather(ctx)
	if err != nil {
		return nil, err
	}
	m.hist = append(m.hist, d.CPUPct)
	if len(m.hist) > histLen {
		m.hist = m.hist[len(m.hist)-histLen:]
	}
	d.CPUHist = append([]float64(nil), m.hist...)
	d.Topo = m.topology(ctx)
	if os.Getenv("SITREP_CAPTURE_FIXTURES") == "1" {
		capture(d)
	}
	return d, nil
}

func gather(ctx context.Context) (Data, error) {
	var d Data
	hi, err := host.InfoWithContext(ctx)
	if err != nil {
		return d, fmt.Errorf("system: host info: %w", err)
	}
	d.Hostname, d.Kernel, d.Arch = hi.Hostname, hi.KernelVersion, hi.KernelArch
	d.Distro = strings.TrimSpace(hi.Platform + " " + hi.PlatformVersion)
	d.Uptime = time.Duration(hi.Uptime) * time.Second
	d.CPUs = runtime.NumCPU()
	if l, err := load.AvgWithContext(ctx); err == nil {
		d.Load = [3]float64{l.Load1, l.Load5, l.Load15}
	}
	// Interval 0 = delta since the previous call, so no blocking sample.
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		d.CPUPct = p[0]
	}
	d.PerCPU = perCPU(ctx)
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		d.MemUsed, d.MemTotal = v.Used, v.Total
	}
	if s, err := mem.SwapMemoryWithContext(ctx); err == nil {
		d.SwapUsed, d.SwapTotal = s.Used, s.Total
	}
	d.TopCPU, d.TopRSS = topProcs(ctx)
	for i, res := range []string{"cpu", "memory", "io"} {
		b, _ := os.ReadFile("/proc/pressure/" + res)
		d.Pressure[i] = ParsePressure(b)
	}
	return d, nil
}

// ParsePressure returns the "some avg10" value of one /proc/pressure file,
// 0 when absent (kernels without PSI, or CONFIG_PSI_DEFAULT_DISABLED).
func ParsePressure(b []byte) float64 {
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				x, _ := strconv.ParseFloat(v, 64)
				return x
			}
		}
	}
	return 0
}

func topProcs(ctx context.Context) (byCPU, byRSS []Proc) {
	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, nil
	}
	all := make([]Proc, 0, len(ps))
	for _, p := range ps {
		if ctx.Err() != nil {
			return nil, nil
		}
		name, _ := p.NameWithContext(ctx)
		c, _ := p.CPUPercentWithContext(ctx)
		var rss uint64
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			rss = mi.RSS
		}
		all = append(all, Proc{PID: p.Pid, Name: name, CPU: c, RSS: rss})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CPU > all[j].CPU })
	byCPU = append(byCPU, all[:min(5, len(all))]...)
	sort.Slice(all, func(i, j int) bool { return all[i].RSS > all[j].RSS })
	byRSS = append(byRSS, all[:min(5, len(all))]...)
	return byCPU, byRSS
}

// The demo fixture is the collected Data as JSON, since gopsutil has no
// command output to capture.
func (m *Module) demoData() (module.Data, error) {
	b, err := sitrep.Fixtures.ReadFile("testdata/fixtures/system/data.json")
	if err != nil {
		return nil, fmt.Errorf("system: demo fixture: %w", err)
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("system: demo fixture: %w", err)
	}
	return d, nil
}

func capture(d Data) {
	d.Hostname = "example"
	b, _ := json.MarshalIndent(d, "", "  ")
	p := filepath.Join("testdata", "fixtures", "system", "data.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o644)
}

// pressureLine is "psi   cpu 0.2%  mem 0.0%  io 0.1%", each value warn-colored
// past 25 and crit past 50: stalls, not utilisation, so the bar thresholds
// would be far too lax.
func pressureLine(sd Data) string {
	s := theme.Current()
	parts := make([]string, 3)
	for i, name := range []string{"cpu", "mem", "io "} {
		v := sd.Pressure[i]
		txt := fmt.Sprintf("%4.1f%%", v)
		switch {
		case v >= 50:
			txt = s.Crit.Render(txt)
		case v >= 25:
			txt = s.Warn.Render(txt)
		}
		parts[i] = name + " " + txt
	}
	return "psi   " + strings.Join(parts, "  ")
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func (*Module) Card(d module.Data, w int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	barW := max(10, min(30, w-22))
	return strings.Join([]string{
		fmt.Sprintf("load  %.2f %.2f %.2f", sd.Load[0], sd.Load[1], sd.Load[2]),
		fmt.Sprintf("mem   %s %3.0f%% %s/%s", ui.Bar(pct(sd.MemUsed, sd.MemTotal), barW), pct(sd.MemUsed, sd.MemTotal), ui.Bytes(sd.MemUsed), ui.Bytes(sd.MemTotal)),
		fmt.Sprintf("cpu   %s %3.0f%%", ui.Sparkline(sd.CPUHist, barW, 100), sd.CPUPct),
		pressureLine(sd),
	}, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	barW := max(10, min(40, w-30))
	lines := []string{
		fmt.Sprintf("%s %s   %s %s   %s %s", s.Dim.Render("distro"), sd.Distro, s.Dim.Render("kernel"), sd.Kernel, s.Dim.Render("arch"), sd.Arch),
		fmt.Sprintf("%s %s   %s %.2f %.2f %.2f", s.Dim.Render("uptime"), ui.Age(sd.Uptime), s.Dim.Render("load"), sd.Load[0], sd.Load[1], sd.Load[2]),
		cpuLine(sd),
		"",
		fmt.Sprintf("%s  %s %3.0f%%", s.Dim.Render("cpu "), ui.Sparkline(sd.CPUHist, barW, 100), sd.CPUPct),
		fmt.Sprintf("%s  %s %3.0f%%  %s / %s", s.Dim.Render("mem "), ui.Bar(pct(sd.MemUsed, sd.MemTotal), barW), pct(sd.MemUsed, sd.MemTotal), ui.Bytes(sd.MemUsed), ui.Bytes(sd.MemTotal)),
		fmt.Sprintf("%s  %s %3.0f%%  %s / %s", s.Dim.Render("swap"), ui.Bar(pct(sd.SwapUsed, sd.SwapTotal), barW), pct(sd.SwapUsed, sd.SwapTotal), ui.Bytes(sd.SwapUsed), ui.Bytes(sd.SwapTotal)),
		pressureLine(sd) + "  " + s.Dim.Render("stall share, last 10s"),
		"",
	}
	cpuRows := make([][]string, 0, 5)
	for _, p := range sd.TopCPU {
		cpuRows = append(cpuRows, []string{fmt.Sprintf("%d", p.PID), p.Name, fmt.Sprintf("%.1f%%", p.CPU)})
	}
	rssRows := make([][]string, 0, 5)
	for _, p := range sd.TopRSS {
		rssRows = append(rssRows, []string{fmt.Sprintf("%d", p.PID), p.Name, ui.Bytes(p.RSS)})
	}
	half := w/2 - 1
	tables := ui.Columns([]string{
		s.Bold.Render("top by cpu") + "\n" + ui.Table([]string{"PID", "PROCESS", "CPU"}, cpuRows, half),
		s.Bold.Render("top by rss") + "\n" + ui.Table([]string{"PID", "PROCESS", "RSS"}, rssRows, half),
	}, 2, w)
	// Whatever height the header and tables leave is the per-cpu block's.
	budget := h - len(lines) - strings.Count(tables, "\n") - 2
	return strings.Join(append(lines, cpuBlock(sd, w, budget), "", tables), "\n")
}

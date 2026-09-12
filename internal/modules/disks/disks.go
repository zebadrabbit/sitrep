// Package disks shows the block tree, mount usage with thresholds, and
// SMART health when root (HANDOFF §5 #5). Mounts live here.
package disks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/shirou/gopsutil/v4/disk"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Device is one node of `lsblk -J`.
type Device struct {
	Name        string   `json:"name"`
	KName       string   `json:"kname"` // kernel name (dm-0 for an lvm volume)
	Size        string   `json:"size"`
	Type        string   `json:"type"`
	FSType      string   `json:"fstype"`
	Mountpoints []string `json:"mountpoints"`
	Model       string   `json:"model"`
	Children    []Device `json:"children,omitempty"`
	SMART       string   `json:"smart,omitempty"` // PASSED, FAILED, "" (not checked)
	// I/O since the previous tick (30s), from /proc/diskstats; HasIO is
	// false on the first tick and for partitions.
	HasIO  bool    `json:"has_io,omitempty"`
	RdRate float64 `json:"rd_rate,omitempty"` // bytes/s
	WrRate float64 `json:"wr_rate,omitempty"`
	Util   float64 `json:"util,omitempty"` // % of the interval the device was busy
}

// Mount is a usage row.
type Mount struct {
	Target string  `json:"target"`
	Source string  `json:"source"`
	FSType string  `json:"fstype"`
	Total  uint64  `json:"total"`
	Used   uint64  `json:"used"`
	Pct    float64 `json:"pct"`
}

// Data is one collection.
type Data struct {
	Devices   []Device  `json:"devices"`
	Arrays    []Array   `json:"arrays,omitempty"` // md, from /proc/mdstat
	Pools     []Pool    `json:"pools,omitempty"`  // zfs, from zpool list
	Mounts    []Mount   `json:"mounts"`
	Worst     *Mount    `json:"worst,omitempty"`
	NDevices  int       `json:"n_devices"`
	SMART     string    `json:"smart"` // worst: FAILED > PASSED > "needs root" > "smartctl missing"
	SMARTAt   time.Time `json:"smart_at,omitempty"`
	Collected time.Time `json:"collected"`
}

type Module struct {
	run     *collect.Runner
	demo    bool
	root    bool
	smart   map[string]string // name → PASSED/FAILED, refreshed every 5m
	smartAt time.Time
	prev    map[string]diskstat // last diskstats sample, for rates
	prevAt  time.Time
}

func New(demo bool) *Module {
	return &Module{run: collect.New("disks", demo), demo: demo, smart: map[string]string{}}
}

func (*Module) ID() string              { return "disks" }
func (*Module) Title() string           { return "Disks" }
func (*Module) Flags() module.Flags     { return module.Flags{NeedsRootForFull: true, Slow: true} }
func (*Module) Interval() time.Duration { return 30 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: block device tree (lsblk) with read/write rate and utilisation per
device from /proc/diskstats deltas (30s average; partitions skipped), md arrays
from /proc/mdstat and zfs pools from zpool list with degraded/rebuild state,
usage per real mount (ext4/xfs/btrfs/
zfs/vfat/nfs/cifs; snap loops and tmpfs skipped), ! at ≥85% and !! at ≥95%,
and SMART overall health per disk when root.
Needs:    lsblk (util-linux). smartctl (smartmontools) + root for SMART; shown
as ◐ otherwise.
Execs:    lsblk -J -o NAME,KNAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL every 30s, zpool list
          when present;
smartctl -H -j /dev/<disk> every 5 minutes.
Interval: 30s (SMART 5m).`
}

func (m *Module) Detect(_ context.Context, env detect.Env) module.Availability {
	m.root = env.Root
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("lsblk") {
		return module.Availability{State: module.Missing, Reason: "lsblk missing (util-linux)"}
	}
	switch {
	case !detect.Has("smartctl"):
		return module.Availability{State: module.Degraded, Reason: "smartctl missing — no SMART"}
	case !env.Root:
		return module.Availability{State: module.NeedsRoot, Reason: "SMART needs root"}
	}
	return module.Availability{State: module.Available, Reason: "lsblk + smartctl"}
}

type lsblkOut struct {
	Blockdevices []Device `json:"blockdevices"`
}

// ParseLsblk decodes lsblk -J.
func ParseLsblk(out []byte) ([]Device, error) {
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var o lsblkOut
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, fmt.Errorf("disks: parse lsblk: %w", err)
	}
	return o.Blockdevices, nil
}

// ParseSmart pulls the pass/fail from smartctl -H -j.
func ParseSmart(out []byte) (string, error) {
	var o struct {
		SmartStatus struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
	}
	if err := json.Unmarshal(out, &o); err != nil {
		return "", fmt.Errorf("disks: parse smartctl: %w", err)
	}
	if o.SmartStatus.Passed {
		return "PASSED", nil
	}
	return "FAILED", nil
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, "lsblk", "-J", "-o", "NAME,KNAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL")
	if err != nil {
		return nil, err
	}
	devs, err := ParseLsblk(res.Stdout)
	if err != nil {
		return nil, err
	}
	d := Data{Collected: time.Now()}
	for _, dev := range devs {
		if dev.Type == "loop" {
			continue // snap squashfs images: not disks
		}
		d.Devices = append(d.Devices, dev)
		if dev.Type == "disk" {
			d.NDevices++
		}
	}
	m.smartPass(ctx, &d)
	if b, err := m.run.ReadFile("/proc/diskstats"); err == nil {
		cur := ParseDiskstats(b)
		rates(d.Devices, m.prev, cur, d.Collected.Sub(m.prevAt))
		m.prev, m.prevAt = cur, d.Collected
	}
	if b, err := m.run.ReadFile("/proc/mdstat"); err == nil {
		d.Arrays = ParseMdstat(b)
	}
	if res, err := m.run.Run(ctx, "zpool", "list", "-H", "-o", "name,health,size,alloc,cap"); err == nil {
		d.Pools = ParseZpoolList(res.Stdout)
	}
	d.Mounts = m.mounts(ctx)
	for i := range d.Mounts {
		if d.Worst == nil || d.Mounts[i].Pct > d.Worst.Pct {
			d.Worst = &d.Mounts[i]
		}
	}
	return d, nil
}

// smartPass refreshes SMART every 5 minutes when root and smartctl exist;
// otherwise records why not.
func (m *Module) smartPass(ctx context.Context, d *Data) {
	switch {
	case !m.demo && !detect.Has("smartctl"):
		d.SMART = "smartctl missing"
		return
	case !m.demo && !m.root:
		d.SMART = "needs root"
		return
	}
	if time.Since(m.smartAt) > 5*time.Minute {
		for i, dev := range d.Devices {
			if dev.Type != "disk" {
				continue
			}
			res, err := m.run.Run(ctx, "smartctl", "-H", "-j", "/dev/"+dev.Name)
			if err != nil && !errors.Is(err, collect.ErrNoFixture) {
				// smartctl exits non-zero on failing disks but still prints JSON.
				if len(res.Stdout) == 0 {
					continue
				}
			}
			if st, err := ParseSmart(res.Stdout); err == nil {
				m.smart[dev.Name] = st
			}
			d.Devices[i].SMART = m.smart[dev.Name]
		}
		m.smartAt = time.Now()
	}
	d.SMART = "PASSED"
	any := false
	for i, dev := range d.Devices {
		d.Devices[i].SMART = m.smart[dev.Name]
		if m.smart[dev.Name] == "FAILED" {
			d.SMART = "FAILED"
		}
		any = any || m.smart[dev.Name] != ""
	}
	if !any {
		d.SMART = "no data"
	}
	d.SMARTAt = m.smartAt
}

var realFS = map[string]bool{"ext4": true, "ext3": true, "xfs": true, "btrfs": true, "zfs": true, "vfat": true,
	"ntfs": true, "exfat": true, "f2fs": true, "nfs": true, "nfs4": true, "cifs": true, "fuse.mergerfs": true}

// mounts reads real filesystems via gopsutil (statfs), or the demo fixture.
func (m *Module) mounts(ctx context.Context) []Mount {
	if m.demo {
		b, err := m.run.ReadFile("/mounts.json")
		if err != nil {
			return nil
		}
		var ms []Mount
		_ = json.Unmarshal(b, &ms)
		return ms
	}
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	var ms []Mount
	for _, p := range parts {
		if !realFS[p.Fstype] || strings.HasPrefix(p.Mountpoint, "/snap/") {
			continue
		}
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		ms = append(ms, Mount{Target: p.Mountpoint, Source: p.Device, FSType: p.Fstype, Total: u.Total, Used: u.Used, Pct: u.UsedPercent})
	}
	if m.run.CaptureDir != "" {
		b, _ := json.MarshalIndent(ms, "", "  ")
		m.run.Capture("/mounts.json", b)
	}
	return ms
}

func (*Module) Card(d module.Data, w int) string {
	dd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	lines := []string{}
	if dd.Worst != nil {
		barW := max(10, min(30, w-30))
		lines = append(lines, fmt.Sprintf("%s %s %3.0f%%  %s", ui.Bar(dd.Worst.Pct, barW), ui.Threshold(dd.Worst.Pct), dd.Worst.Pct, dd.Worst.Target))
	}
	smart := s.Dim.Render(dd.SMART)
	switch dd.SMART {
	case "PASSED":
		smart = s.OK.Render(s.Glyph.OK + " SMART passed")
	case "FAILED":
		smart = s.Crit.Render(s.Glyph.Crit + " SMART FAILED")
	case "needs root", "smartctl missing":
		smart = s.Warn.Render(s.Glyph.Degraded + " SMART " + dd.SMART)
	}
	lines = append(lines, fmt.Sprintf("%s disks  %d mounts  %s", s.Bold.Render(fmt.Sprint(dd.NDevices)), len(dd.Mounts), smart))
	for _, a := range dd.Arrays {
		if a.Degraded || a.State != "active" {
			lines = append(lines, s.Crit.Render(fmt.Sprintf("%s %s %s %s", s.Glyph.Crit, a.Name, a.Level, a.Status))+" "+s.Dim.Render(a.Progress))
		}
	}
	for _, p := range dd.Pools {
		if p.Health != "ONLINE" {
			lines = append(lines, s.Crit.Render(fmt.Sprintf("%s %s %s", s.Glyph.Crit, p.Name, p.Health)))
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
	barW := max(10, min(30, w/4))
	rows := [][]string{}
	for _, mt := range dd.Mounts {
		rows = append(rows, []string{mt.Target, ui.Bar(mt.Pct, barW) + " " + ui.Threshold(mt.Pct), fmt.Sprintf("%3.0f%%", mt.Pct), ui.Bytes(mt.Used) + "/" + ui.Bytes(mt.Total), s.Dim.Render(mt.Source + " " + mt.FSType)})
	}
	out := []string{s.Bold.Render("mounts"), ui.Table([]string{"TARGET", "USAGE", "", "USED/TOTAL", "SOURCE"}, rows, w), ""}
	out = append(out, raidLines(dd, w)...)
	out = append(out, s.Bold.Render("block devices")+"  "+s.Dim.Render("SMART "+dd.SMART+" · I/O avg over 30s"))
	// The I/O slot exists only once there are two samples (never on the
	// first tick, never in demo), so the tree does not carry a blank gap.
	showIO := false
	for _, dev := range dd.Devices {
		showIO = showIO || hasIO(dev)
	}
	for _, dev := range dd.Devices {
		out = append(out, tree(dev, "", w, showIO)...)
	}
	res := strings.Join(out, "\n")
	if h > 0 {
		res = strings.Join(strings.Split(res, "\n")[:min(h, strings.Count(res, "\n")+1)], "\n")
	}
	return res
}

func hasIO(d Device) bool {
	if d.HasIO {
		return true
	}
	for _, c := range d.Children {
		if hasIO(c) {
			return true
		}
	}
	return false
}

func tree(d Device, indent string, w int, showIO bool) []string {
	s := theme.Current()
	smart := ""
	switch d.SMART {
	case "PASSED":
		smart = "  " + s.OK.Render(s.Glyph.OK)
	case "FAILED":
		smart = "  " + s.Crit.Render(s.Glyph.Crit+" SMART FAILED")
	}
	extra := d.Model
	if extra == "" {
		extra = d.FSType
	}
	mp := ""
	for _, m := range d.Mountpoints {
		if m != "" {
			mp = "  " + m
		}
	}
	// A fixed-width I/O slot so the model and mountpoint line up across rows.
	io := ""
	if showIO {
		io = strings.Repeat(" ", 26)
	}
	if d.HasIO {
		util := fmt.Sprintf("%3.0f%%", d.Util)
		if d.Util >= 90 {
			util = s.Warn.Render(util)
		}
		io = fmt.Sprintf("↓%-8s ↑%-8s %s ", ui.Bytes(uint64(d.RdRate))+"/s", ui.Bytes(uint64(d.WrRate))+"/s", util)
	}
	line := fmt.Sprintf("%s%-12s %7s  %-5s %s %s%s%s", indent, d.Name, d.Size, d.Type, io, s.Dim.Render(extra), mp, smart)
	out := []string{line}
	for i, c := range d.Children {
		branch := "├─"
		if i == len(d.Children)-1 {
			branch = "└─"
		}
		out = append(out, tree(c, indent+branch, w, showIO)...)
	}
	return out
}

// raidLines is the md arrays and zfs pools section, absent when the box has
// neither. A degraded array or non-ONLINE pool is crit; a rebuild is warn.
func raidLines(dd Data, w int) []string {
	if len(dd.Arrays) == 0 && len(dd.Pools) == 0 {
		return nil
	}
	s := theme.Current()
	rows := [][]string{}
	for _, a := range dd.Arrays {
		st := s.OK.Render(s.Glyph.OK) + " " + a.State
		switch {
		case a.Degraded || a.State != "active":
			st = s.Crit.Render(s.Glyph.Crit + " degraded")
		case a.Progress != "":
			st = s.Warn.Render(s.Glyph.Warn + " " + a.Progress)
		}
		rows = append(rows, []string{a.Name, s.Dim.Render(a.Level), st, a.Status, s.Dim.Render(strings.Join(a.Members, " ")), a.Progress})
	}
	for _, p := range dd.Pools {
		st := s.OK.Render(s.Glyph.OK) + " " + p.Health
		if p.Health != "ONLINE" {
			st = s.Crit.Render(s.Glyph.Crit + " " + p.Health)
		}
		rows = append(rows, []string{p.Name, s.Dim.Render("zpool"), st, p.Cap, s.Dim.Render(p.Alloc + " of " + p.Size), ""})
	}
	return []string{s.Bold.Render("raid"), ui.Table([]string{"NAME", "LEVEL", "STATE", "STATUS", "MEMBERS", ""}, rows, w), ""}
}

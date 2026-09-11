// Package network shows interfaces, addresses, rates as a mirrored braille
// graph, the default route and DNS (HANDOFF §5 #6).
package network

import (
	"context"
	"encoding/json"
	"fmt"
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

const histLen = 60

// Iface is one interface.
type Iface struct {
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Kind    string   `json:"kind"` // wireguard, tailscale, bridge, veth, loopback, ether, ...
	Addrs   []string `json:"addrs"`
	MAC     string   `json:"mac,omitempty"`
	MTU     int      `json:"mtu"`
	RxBytes uint64   `json:"rx_bytes"`
	TxBytes uint64   `json:"tx_bytes"`
	RxRate  float64  `json:"rx_rate"` // bytes/s since last collection
	TxRate  float64  `json:"tx_rate"`
	Default bool     `json:"default"` // carries the default route
}

// Data is one collection.
type Data struct {
	Ifaces    []Iface   `json:"ifaces"`
	Gateway   string    `json:"gateway"`
	DefaultIf string    `json:"default_if"`
	PrimaryIP string    `json:"primary_ip"`
	DNS       []string  `json:"dns"`
	RxHist    []float64 `json:"rx_hist"` // default iface, bytes/s
	TxHist    []float64 `json:"tx_hist"`
	Collected time.Time `json:"collected"`
}

type Module struct {
	run    *collect.Runner
	demo   bool
	prev   map[string][2]uint64
	prevAt time.Time
	rx, tx []float64
	ui     uiState
}

type uiState struct{ sel, visible int }

func New(demo bool) *Module {
	return &Module{run: collect.New("network", demo), demo: demo, prev: map[string][2]uint64{}}
}

func (*Module) ID() string              { return "network" }
func (*Module) Title() string           { return "Network" }
func (*Module) Flags() module.Flags     { return module.Flags{} }
func (*Module) Interval() time.Duration { return 2 * time.Second }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: interfaces with addresses, state, kind (wireguard/tailscale/bridge/
veth labeled), rx/tx rates from /proc/net/dev deltas, the default route and
gateway, DNS servers. The default-route interface gets a 60-sample mirrored
braille graph: rx up, tx down.
Needs:    /proc/net/dev, ip (iproute2). No root.
Execs:    ip -j addr, ip -j route, ip -d -j link. Reads /etc/resolv.conf.
Interval: 2s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("ip") {
		return module.Availability{State: module.Degraded, Reason: "ip missing (iproute2); counters only"}
	}
	return module.Availability{State: module.Available, Reason: "ip + /proc/net/dev"}
}

func (m *Module) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "j", "down":
			m.ui.sel++
		case "k", "up":
			m.ui.sel--
		}
		m.ui.sel = max(0, min(m.ui.sel, m.ui.visible-1))
	}
	return nil
}

// ip -j addr shapes.
type ipAddr struct {
	Ifname    string `json:"ifname"`
	Operstate string `json:"operstate"`
	LinkType  string `json:"link_type"`
	Address   string `json:"address"`
	MTU       int    `json:"mtu"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
	} `json:"addr_info"`
	Linkinfo struct {
		InfoKind string `json:"info_kind"`
	} `json:"linkinfo"`
}

type ipRoute struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
	Metric  int    `json:"metric"`
}

// ParseAddr decodes `ip -d -j addr` (the -d adds linkinfo for kinds).
func ParseAddr(out []byte) ([]Iface, error) {
	var raw []ipAddr
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("network: parse ip addr: %w", err)
	}
	var ifs []Iface
	for _, r := range raw {
		i := Iface{Name: r.Ifname, State: strings.ToLower(r.Operstate), MAC: r.Address, MTU: r.MTU, Kind: kind(r)}
		for _, a := range r.AddrInfo {
			if a.Scope == "link" {
				continue
			}
			i.Addrs = append(i.Addrs, fmt.Sprintf("%s/%d", a.Local, a.Prefixlen))
		}
		ifs = append(ifs, i)
	}
	return ifs, nil
}

func kind(r ipAddr) string {
	switch {
	case r.Linkinfo.InfoKind != "":
		if r.Linkinfo.InfoKind == "tun" && strings.HasPrefix(r.Ifname, "tailscale") {
			return "tailscale"
		}
		return r.Linkinfo.InfoKind
	case r.LinkType == "loopback":
		return "loopback"
	case strings.HasPrefix(r.Ifname, "tailscale"):
		return "tailscale"
	}
	return r.LinkType
}

// ParseRoute returns the best default route (lowest metric).
func ParseRoute(out []byte) (gw, dev string, err error) {
	var rs []ipRoute
	if err := json.Unmarshal(out, &rs); err != nil {
		return "", "", fmt.Errorf("network: parse ip route: %w", err)
	}
	best := -1
	for _, r := range rs {
		if r.Dst != "default" {
			continue
		}
		if best < 0 || r.Metric < best {
			best, gw, dev = r.Metric, r.Gateway, r.Dev
		}
	}
	return gw, dev, nil
}

// ParseNetDev reads /proc/net/dev into name → {rx, tx} bytes.
func ParseNetDev(out []byte) map[string][2]uint64 {
	c := map[string][2]uint64{}
	for _, line := range strings.Split(string(out), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		var rx, tx uint64
		fmt.Sscan(f[0], &rx) //nolint:errcheck
		fmt.Sscan(f[8], &tx) //nolint:errcheck
		c[strings.TrimSpace(name)] = [2]uint64{rx, tx}
	}
	return c
}

// ParseResolv pulls nameservers from resolv.conf.
func ParseResolv(out []byte) []string {
	var dns []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "nameserver "); ok && !seen[v] {
			seen[v] = true
			dns = append(dns, strings.TrimSpace(v))
		}
	}
	return dns
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	d := Data{Collected: m.now()}
	addr, err := m.run.Run(ctx, "ip", "-d", "-j", "addr")
	if err != nil {
		return nil, err
	}
	if d.Ifaces, err = ParseAddr(addr.Stdout); err != nil {
		return nil, err
	}
	if route, err := m.run.Run(ctx, "ip", "-j", "route"); err == nil {
		d.Gateway, d.DefaultIf, _ = ParseRoute(route.Stdout)
	}
	if b, err := m.run.ReadFile("/etc/resolv.conf"); err == nil {
		d.DNS = ParseResolv(b)
	}
	if b, err := m.run.ReadFile("/proc/net/dev"); err == nil {
		m.rates(ParseNetDev(b), &d)
	}
	for i := range d.Ifaces {
		in := &d.Ifaces[i]
		in.Default = in.Name == d.DefaultIf
		if in.Default && len(in.Addrs) > 0 {
			d.PrimaryIP = strings.Split(in.Addrs[0], "/")[0]
		}
	}
	sort.SliceStable(d.Ifaces, func(i, j int) bool { return rank(d.Ifaces[i]) < rank(d.Ifaces[j]) })
	d.RxHist, d.TxHist = append([]float64(nil), m.rx...), append([]float64(nil), m.tx...)
	return d, nil
}

// now is real time live; demo derives the tick from a fixed base so rates
// come out stable (a demo has one counter sample, so rates are 0 anyway).
func (m *Module) now() time.Time { return time.Now() }

// rates turns counter deltas into bytes/s and pushes the default iface's
// into the ring buffers.
func (m *Module) rates(cur map[string][2]uint64, d *Data) {
	dt := d.Collected.Sub(m.prevAt).Seconds()
	for i := range d.Ifaces {
		in := &d.Ifaces[i]
		c, ok := cur[in.Name]
		if !ok {
			continue
		}
		in.RxBytes, in.TxBytes = c[0], c[1]
		if p, ok := m.prev[in.Name]; ok && dt > 0 && c[0] >= p[0] && c[1] >= p[1] {
			in.RxRate = float64(c[0]-p[0]) / dt
			in.TxRate = float64(c[1]-p[1]) / dt
		}
		if in.Name == d.DefaultIf && !m.prevAt.IsZero() {
			m.rx = push(m.rx, in.RxRate)
			m.tx = push(m.tx, in.TxRate)
		}
	}
	m.prev, m.prevAt = cur, d.Collected
}

func push(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > histLen {
		h = h[len(h)-histLen:]
	}
	return h
}

// rank: default route first, then up ethernet/vpn, then bridges, then veth/down, lo last.
func rank(i Iface) int {
	switch {
	case i.Default:
		return 0
	case i.Kind == "loopback":
		return 9
	case i.Kind == "veth":
		return 7
	case i.Kind == "bridge":
		return 5
	case i.State != "up" && i.State != "unknown":
		return 6
	case i.Kind == "wireguard" || i.Kind == "tailscale" || i.Kind == "tun":
		return 2
	}
	return 1
}

func rate(bps float64) string { return ui.Bytes(uint64(bps)) + "/s" }

func (*Module) Card(d module.Data, w int) string {
	nd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	var def *Iface
	for i := range nd.Ifaces {
		if nd.Ifaces[i].Default {
			def = &nd.Ifaces[i]
		}
	}
	lines := []string{}
	if def != nil {
		lines = append(lines, fmt.Sprintf("%s  %s via %s", s.Bold.Render(nd.PrimaryIP), def.Name, nd.Gateway))
		lines = append(lines, fmt.Sprintf("↓ %-10s ↑ %-10s", rate(def.RxRate), rate(def.TxRate)))
	} else {
		lines = append(lines, s.Warn.Render(s.Glyph.Degraded+" no default route"))
	}
	if len(nd.RxHist) > 1 {
		lines = append(lines, ui.Mirror(nd.RxHist, nd.TxHist, max(10, min(40, w-4)), 1))
	}
	return strings.Join(lines, "\n")
}

func (m *Module) View(d module.Data, w, h int) string {
	nd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	head := kv("default", nd.DefaultIf+" via "+nd.Gateway) + "   " + kv("dns", strings.Join(nd.DNS, ", "))
	rows := [][]string{}
	for _, in := range nd.Ifaces {
		g := s.Dim.Render(s.Glyph.Fail)
		if in.State == "up" || in.State == "unknown" && in.Kind != "loopback" {
			g = s.OK.Render(s.Glyph.OK)
		}
		name := in.Name
		if in.Default {
			name = s.Bold.Render(name)
		}
		rows = append(rows, []string{g + " " + name, s.Dim.Render(in.Kind), strings.Join(in.Addrs, " "), "↓" + rate(in.RxRate), "↑" + rate(in.TxRate)})
	}
	m.ui.visible = len(rows)
	if m.ui.sel >= len(rows) {
		m.ui.sel = max(0, len(rows)-1)
	}
	graphRows := 3
	listH := len(rows) + 1
	if h > 0 {
		listH = max(3, h-2*graphRows-4)
	}
	lo, hi := 0, len(rows)
	if len(rows) > listH-1 {
		if m.ui.sel >= listH-1 {
			lo = m.ui.sel - listH + 2
		}
		hi = min(len(rows), lo+listH-1)
	}
	table := ui.Table([]string{"  IFACE", "KIND", "ADDRESSES", "RX", "TX"}, rows[lo:hi], w-1)
	lines := strings.Split(table, "\n")
	lines[0] = " " + lines[0]
	for i := range lines[1:] {
		cursor := " "
		if h > 0 && lo+i == m.ui.sel {
			cursor = s.Accent.Render("▎")
		}
		lines[i+1] = cursor + lines[i+1]
	}
	out := []string{head, ""}
	out = append(out, lines...)
	if hidden := len(rows) - (hi - lo); hidden > 0 {
		out = append(out, s.Dim.Render(fmt.Sprintf("  … %d more", hidden)))
	}
	out = append(out, "", s.Bold.Render(nd.DefaultIf)+"  "+s.OK.Render("▲ rx")+" "+s.Accent.Render("▼ tx")+"  "+s.Dim.Render(peakStr(nd)))
	out = append(out, ui.Mirror(nd.RxHist, nd.TxHist, max(10, w-2), graphRows))
	res := strings.Join(out, "\n")
	if h > 0 {
		res = strings.Join(strings.Split(res, "\n")[:min(h, strings.Count(res, "\n")+1)], "\n")
	}
	return res
}

func peakStr(nd Data) string {
	p := 0.0
	for _, v := range append(append([]float64{}, nd.RxHist...), nd.TxHist...) {
		p = max(p, v)
	}
	return "peak " + rate(p) + " · last 2 min"
}

func kv(k, v string) string { return theme.Current().Dim.Render(k+" ") + v }

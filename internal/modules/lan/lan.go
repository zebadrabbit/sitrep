// Package lan lists the devices this box shares a segment with, folded by
// MAC and labelled from the IEEE vendor table. Everything here is read out
// of the kernel's neighbor and socket tables: no host is ever probed, swept
// or scanned (HANDOFF §10.3).
package lan

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules/network"
	"github.com/zebadrabbit/sitrep/internal/modules/ports"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Data is one collection.
type Data struct {
	Hosts     []Host   `json:"hosts"`
	Subnets   []Subnet `json:"subnets"`
	Reachable int      `json:"reachable"`
	Random    int      `json:"random"`
	Source    string   `json:"source"` // "ip neigh" or "/proc/net/arp"
}

type Module struct {
	run     *collect.Runner
	vendors Vendors
	names   map[string]string
}

func New(demo bool) *Module {
	m := &Module{run: collect.New("lan", demo)}
	dir := config.Dir()
	if demo {
		dir = "" // screenshots must not pick up the developer's device names
	}
	// A broken lan.toml costs the device names, never the module.
	v, names, _ := LoadVendors(dir)
	m.vendors, m.names = v, names
	return m
}

func (*Module) ID() string              { return "lan" }
func (*Module) Title() string           { return "LAN" }
func (*Module) Flags() module.Flags     { return module.Flags{} }
func (*Module) Interval() time.Duration { return 30 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: devices sharing a segment with this box, from the kernel's
neighbor (ARP/NDP) table. Rows sharing a MAC fold into one host, so a device
reached over IPv4 and a link-local, or seen from two NICs, counts once.
Vendor comes from the embedded IEEE MA-L table; a MAC whose second nibble is
2, 6, A or E is flagged randomized (phones rotate these per network, so one
device reappears as a new unknown host after a reset). TALKS is read out of
the socket table: <-port is a connection they opened to a service here,
->port one we opened to them.
Nothing is scanned. sitrep sends no packets to any of these hosts; a device
that has not spoken to this box does not appear at all.
Names:    ~/.config/sitrep/lan.toml overrides a label by MAC or IP:
            [names]
            "b8:27:eb:11:22:33" = "tundra"
            "10.0.0.118" = "prusa"
Needs:    nothing. iproute2 gives state and IPv6; without it /proc/net/arp
          still lists IPv4 neighbors.
Execs:    ip -j neigh, ip -j route, ss -tunaH, ss -tulnH
Interval: 30s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("ip") {
		return module.Availability{State: module.Degraded, Reason: "ip missing (iproute2); /proc/net/arp only, no IPv6"}
	}
	return module.Availability{State: module.Available, Reason: "ip neigh"}
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	d := Data{Source: "ip neigh"}
	var ns []Neighbor
	if res, err := m.run.Run(ctx, "ip", "-j", "neigh"); err == nil {
		ns = ParseNeigh(res.Stdout)
	}
	if len(ns) == 0 {
		b, err := m.run.ReadFile("/proc/net/arp")
		if err != nil {
			return nil, fmt.Errorf("lan: no neighbor table: %w", err)
		}
		ns, d.Source = ParseProcARP(b), "/proc/net/arp"
	}

	var gateway string
	if res, err := m.run.Run(ctx, "ip", "-j", "route"); err == nil {
		d.Subnets = ParseLinkSubnets(res.Stdout)
		gateway, _, _ = network.ParseRoute(res.Stdout)
	}

	d.Hosts = Fold(ns)
	Annotate(d.Hosts, d.Subnets, m.vendors, m.names, gateway)

	// -n and no -p: no name resolution, no root needed. Without the listener
	// set there is no honest way to tell inbound from outbound, so TALKS stays
	// empty rather than pointing every arrow the same way.
	if lis, err := m.run.Run(ctx, "ss", "-tulnH"); err == nil {
		if res, err := m.run.Run(ctx, "ss", "-tunaH"); err == nil {
			AttachPeers(d.Hosts, ports.ParseEstablished(res.Stdout), ListeningPorts(lis.Stdout))
		}
	}

	for _, h := range d.Hosts {
		if rank(h.State) <= rank("probe") {
			d.Reachable++
		}
		// A container's MAC is locally administered by construction; counting
		// it would inflate the number that means "phone rotating its MAC".
		if h.Random && !h.Virtual {
			d.Random++
		}
	}
	return d, nil
}

// glyph maps a neighbor state onto the allowed status set: a device that
// answered recently is ok, one the kernel has not re-verified is degraded,
// one that never answered is failed.
func glyph(state string, s theme.Styles) string {
	switch {
	case rank(state) <= rank("noarp"):
		return s.OK.Render(s.Glyph.OK)
	case rank(state) <= rank("stale"):
		return s.Warn.Render(s.Glyph.Degraded)
	default:
		return s.Dim.Render(s.Glyph.Fail)
	}
}

// label is what the DEVICE column shows: the user's name, else the vendor,
// else a note that the MAC is randomized and names nothing.
func label(h Host, s theme.Styles) string {
	switch {
	case h.Name != "":
		return s.Bold.Render(h.Name)
	case h.Vendor != "":
		return h.Vendor
	case h.Virtual:
		return s.Dim.Render("(virtual)")
	case h.Random:
		return s.Dim.Render("(randomized)")
	default:
		return s.Dim.Render("—")
	}
}

func (*Module) Card(d module.Data, w int) string {
	ld, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	lines := []string{fmt.Sprintf("%s hosts  %s up  %s randomized",
		s.Bold.Render(strconv.Itoa(len(ld.Hosts))),
		s.OK.Render(strconv.Itoa(ld.Reachable)),
		s.Dim.Render(strconv.Itoa(ld.Random)))}
	// The three the box actually talks to say more than the three lowest IPs.
	n := 0
	for _, h := range ld.Hosts {
		if len(h.In)+len(h.Out) == 0 || n == 3 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s", glyph(h.State, s), h.IP, s.Dim.Render(talks(h, s, true))))
		n++
	}
	if n == 0 && len(ld.Hosts) == 0 {
		lines = append(lines, s.Dim.Render("no neighbors seen"))
	}
	return strings.Join(lines, "\n")
}

// talks renders the ports this host and the box have in common. plain drops
// the color so the card can dim the whole string.
func talks(h Host, s theme.Styles, plain bool) string {
	var parts []string
	for _, p := range h.In {
		parts = append(parts, "<-"+strconv.Itoa(p))
	}
	for _, p := range h.Out {
		t := "->" + strconv.Itoa(p)
		if !plain {
			t = s.Dim.Render(t)
		}
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 4 {
		parts = append(parts[:4], fmt.Sprintf("+%d", len(parts)-4))
	}
	return strings.Join(parts, " ")
}

func (m *Module) View(d module.Data, w, h int) string {
	ld, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	if len(ld.Hosts) == 0 {
		return s.Dim.Render("no neighbors in the ARP table yet")
	}
	wide := w >= 90

	var out []string
	for _, g := range groups(ld) {
		head := s.Bold.Render(g.title)
		if g.iface != "" {
			head += "  " + s.Dim.Render(g.iface)
		}
		out = append(out, head, hostTable(g.hosts, s, wide, w), "")
	}
	out = append(out, s.Dim.Render(fmt.Sprintf("%d hosts  ·  %d reachable  ·  %d randomized  ·  %s, nothing scanned",
		len(ld.Hosts), ld.Reachable, ld.Random, ld.Source)))

	body := strings.Join(out, "\n")
	if h > 0 {
		if lines := strings.Split(body, "\n"); len(lines) > h {
			body = strings.Join(lines[:h], "\n")
		}
	}
	return body
}

func hostTable(hosts []Host, s theme.Styles, wide bool, w int) string {
	header := []string{"  IP", "DEVICE", "STATE", "TALKS"}
	if wide {
		header = []string{"  IP", "DEVICE", "MAC", "STATE", "TALKS"}
	}
	rows := make([][]string, 0, len(hosts))
	for _, h := range hosts {
		ip := h.IP
		if len(h.Extra) > 0 {
			ip += s.Dim.Render(fmt.Sprintf(" +%d", len(h.Extra)))
		}
		if h.Gateway {
			ip += " " + s.Dim.Render("gw")
		}
		mac := h.MAC
		switch {
		case mac == "":
			mac = s.Dim.Render("—")
		case h.Shared:
			mac = s.Dim.Render(mac) // several addresses answer on it
		case h.Random && !h.Virtual:
			mac = s.Warn.Render(mac)
		}
		state := h.State
		if state == "" {
			state = "unknown"
		}
		row := []string{glyph(h.State, s) + " " + ip, label(h, s), s.Dim.Render(state), talks(h, s, false)}
		if wide {
			row = []string{row[0], row[1], mac, row[2], row[3]}
		}
		rows = append(rows, row)
	}
	return ui.Table(header, rows, w)
}

type group struct {
	title, iface string
	hosts        []Host
}

// groups splits hosts by on-link subnet, in the order the routes list them,
// with anything outside every CIDR last under its interface.
func groups(d Data) []group {
	var gs []group
	seen := map[string]int{}
	for _, s := range d.Subnets {
		if _, ok := seen[s.CIDR]; ok {
			continue
		}
		seen[s.CIDR] = len(gs)
		gs = append(gs, group{title: s.CIDR, iface: s.Iface})
	}
	var loose []Host
	for _, h := range d.Hosts {
		if i, ok := seen[h.Subnet]; ok && h.Subnet != "" {
			gs[i].hosts = append(gs[i].hosts, h)
			continue
		}
		loose = append(loose, h)
	}
	out := gs[:0]
	for _, g := range gs {
		if len(g.hosts) > 0 {
			out = append(out, g)
		}
	}
	if len(loose) > 0 {
		out = append(out, group{title: "other", hosts: loose})
	}
	// Real segments first: a bridge with one container on it should not push
	// the LAN off the bottom of a 30-line terminal.
	slices.SortStableFunc(out, func(a, b group) int {
		av, bv := VirtualIface(a.iface), VirtualIface(b.iface)
		if av == bv {
			return 0
		}
		if bv {
			return -1
		}
		return 1
	})
	return out
}

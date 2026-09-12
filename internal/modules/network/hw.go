package network

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/zebadrabbit/sitrep/internal/collect"
)

// Firewall is whichever of ufw / nftables / firewalld is the active unit,
// plus what its rules say when we are allowed to read them.
type Firewall struct {
	Backend   string `json:"backend,omitempty"` // "" = none active
	Active    bool   `json:"active"`
	Enabled   bool   `json:"enabled"`
	Defaults  string `json:"defaults,omitempty"` // ufw: "deny in, allow out"
	Rules     int    `json:"rules"`              // ufw rules, or nft chains
	NeedsRoot bool   `json:"needs_root"`         // rules refused unprivileged
}

// ParseFirewallUnits reads `systemctl show <units> -p Id,ActiveState,UnitFileState`
// blocks; the first active unit wins. ponytail: firewalld is detected but its
// rules are not read (no firewalld here to capture from); add firewall-cmd
// --list-all when a box needs it.
func ParseFirewallUnits(out []byte) Firewall {
	units := collect.ShowProps(out)
	for _, id := range []string{"ufw", "nftables", "firewalld"} {
		if p := units[id]; p["ActiveState"] == "active" {
			return Firewall{Backend: id, Active: true, Enabled: p["UnitFileState"] == "enabled"}
		}
	}
	return Firewall{}
}

// ParseUfwStatus reads `ufw status verbose`: the Default line and the rule
// rows under the "--" header.
func ParseUfwStatus(out []byte) (defaults string, rules int) {
	inRules := false
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "Default:"):
			// "deny (incoming), allow (outgoing), deny (routed)"
			var parts []string
			for _, p := range strings.Split(strings.TrimPrefix(line, "Default:"), ",") {
				f := strings.Fields(p)
				if len(f) == 2 {
					dir := map[string]string{"(incoming)": "in", "(outgoing)": "out", "(routed)": "fwd"}[f[1]]
					if dir != "fwd" {
						parts = append(parts, f[0]+" "+dir)
					}
				}
			}
			defaults = strings.Join(parts, ", ")
		case strings.HasPrefix(line, "--"):
			inRules = true
		case inRules && strings.TrimSpace(line) != "":
			rules++
		}
	}
	return defaults, rules
}

// ParseNftRuleset counts chains in `nft list ruleset`; rules per chain vary
// too much to be one honest number.
func ParseNftRuleset(out []byte) int {
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "chain ") {
			n++
		}
	}
	return n
}

// ParseLspci maps PCI address → "Vendor Device" for network-class devices,
// from `lspci -mm -nn`. Vendor is its first word: "Intel Corporation [8086]"
// is just Intel on an 80-column line.
func ParseLspci(out []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(line, "\"", 7)
		if len(f) < 7 || !netClass(f[1]) {
			continue
		}
		vendor := strings.Fields(stripID(f[3]))
		if len(vendor) == 0 {
			continue
		}
		m["0000:"+strings.TrimSpace(f[0])] = vendor[0] + " " + stripID(f[5])
	}
	return m
}

func netClass(class string) bool {
	return strings.Contains(class, "Ethernet") || strings.Contains(class, "Network") || strings.Contains(class, "Wireless")
}

// stripID drops the trailing " [14e4]".
func stripID(s string) string {
	if i := strings.LastIndex(s, " ["); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// linkStr renders sysfs speed (Mb/s) and duplex as "1G full"; "" when the
// link is down (speed -1) or the file is missing.
func linkStr(speed, duplex string) string {
	mb, err := strconv.Atoi(strings.TrimSpace(speed))
	if err != nil || mb <= 0 {
		return ""
	}
	var s string
	switch {
	case mb >= 1000 && mb%1000 == 0:
		s = strconv.Itoa(mb/1000) + "G"
	case mb >= 1000:
		s = strconv.FormatFloat(float64(mb)/1000, 'f', 1, 64) + "G"
	default:
		s = strconv.Itoa(mb) + "M"
	}
	if d := strings.TrimSpace(duplex); d == "full" || d == "half" {
		s += " " + d
	}
	return s
}

// hardware fills Driver, Link and Model for interfaces that have a PCI/USB
// device behind them; bridges, veths and tunnels have none and stay blank.
func (m *Module) hardware(ifaces []Iface) {
	for i := range ifaces {
		in := &ifaces[i]
		base := "/sys/class/net/" + in.Name
		dev, err := m.run.Readlink(base + "/device")
		if err != nil {
			continue
		}
		if drv, err := m.run.Readlink(base + "/device/driver"); err == nil {
			in.Driver = path.Base(drv)
		}
		speed, _ := m.run.ReadFile(base + "/speed")
		duplex, _ := m.run.ReadFile(base + "/duplex")
		in.Link = linkStr(string(speed), string(duplex))
		in.Model = m.pci()[path.Base(dev)]
	}
}

// pci runs lspci once; the cards do not change while sitrep is up.
func (m *Module) pci() map[string]string {
	if m.pciNames == nil {
		m.pciNames = map[string]string{}
		if res, err := m.run.Run(context.Background(), "lspci", "-mm", "-nn"); err == nil {
			m.pciNames = ParseLspci(res.Stdout)
		}
	}
	return m.pciNames
}

// firewall is the unit check every tick, then the rules when the backend
// lets us read them: unprivileged ufw and nft both refuse, which is the
// ◐ needs-root case rather than an error.
func (m *Module) firewall(ctx context.Context) Firewall {
	res, err := m.run.RunNamed(ctx, "systemctl_show_firewall.txt", "systemctl", "show",
		"ufw.service", "nftables.service", "firewalld.service", "-p", "Id,ActiveState,UnitFileState")
	if err != nil {
		return Firewall{}
	}
	fw := ParseFirewallUnits(res.Stdout)
	switch fw.Backend {
	case "ufw":
		st, err := m.run.Run(ctx, "ufw", "status", "verbose")
		if err != nil {
			fw.NeedsRoot = !errors.Is(err, collect.ErrNoFixture) || m.demo
			break
		}
		fw.Defaults, fw.Rules = ParseUfwStatus(st.Stdout)
	case "nftables":
		st, err := m.run.Run(ctx, "nft", "list", "ruleset")
		if err != nil {
			fw.NeedsRoot = true
			break
		}
		fw.Rules = ParseNftRuleset(st.Stdout)
	}
	return fw
}

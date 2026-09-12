package lan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zebadrabbit/sitrep/internal/modules/ports"
)

const neighJSON = `[{"dst":"10.0.0.1","dev":"eth0","lladdr":"b4:75:0e:00:00:01","state":["REACHABLE"]},
{"dst":"fe80::b675:eff:fe00:1","dev":"eth0","lladdr":"b4:75:0e:00:00:01","state":["STALE"]},
{"dst":"10.0.0.5","dev":"eth0","state":["FAILED"]},
{"dst":"10.0.0.7","dev":"eth0","lladdr":"76:fe:3c:00:00:02","state":["STALE"]},
{"dst":"172.17.0.9","dev":"docker0","lladdr":"02:42:ac:00:00:03","state":["REACHABLE"]},
{"dst":"","dev":"eth0","lladdr":"aa:bb:cc:00:00:04","state":["REACHABLE"]}]`

func TestParseNeigh(t *testing.T) {
	ns := ParseNeigh([]byte(neighJSON))
	if len(ns) != 5 {
		t.Fatalf("got %d neighbors, want 5 (the empty dst is dropped)", len(ns))
	}
	if ns[0].State != "reachable" || ns[0].MAC != "b4:75:0e:00:00:01" {
		t.Errorf("first row = %+v", ns[0])
	}
	if ns[2].MAC != "" {
		t.Errorf("a FAILED entry has no lladdr, got %q", ns[2].MAC)
	}
}

func TestParseNeighSurvivesBadInput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"garbage":   "not json at all",
		"emptyarr":  "[]",
		"truncated": `[{"dst":"10.0.0.1","dev":"eth0"`,
	} {
		if got := ParseNeigh([]byte(in)); len(got) != 0 {
			t.Errorf("%s: got %d rows, want 0", name, len(got))
		}
	}
}

const procARP = `IP address       HW type     Flags       HW address            Mask     Device
10.0.0.1         0x1         0x2         b4:75:0e:00:00:01     *        eth0
10.0.0.5         0x1         0x0         00:00:00:00:00:00     *        eth0
short line
10.0.0.7         0x1         0x2         76:fe:3c:00:00:02     *        eth0
`

func TestParseProcARP(t *testing.T) {
	ns := ParseProcARP([]byte(procARP))
	if len(ns) != 3 {
		t.Fatalf("got %d rows, want 3 (header and the short line skipped)", len(ns))
	}
	if ns[1].MAC != "" || ns[1].State != "failed" {
		t.Errorf("an incomplete entry should have no MAC and be failed, got %+v", ns[1])
	}
}

// Folding is the whole reason one device with four addresses is one row —
// and the reason three set-top boxes behind one bridge MAC are not.
func TestFoldKeepsDistinctIPv4OnAsharedMAC(t *testing.T) {
	hosts := Fold(ParseNeigh([]byte(neighJSON)))
	byIP := map[string]Host{}
	for _, h := range hosts {
		byIP[h.IP] = h
	}
	gw, ok := byIP["10.0.0.1"]
	if !ok {
		t.Fatal("no host for 10.0.0.1")
	}
	if len(gw.Extra) != 1 || gw.Extra[0] != "fe80::b675:eff:fe00:1" {
		t.Errorf("the link-local should fold into the IPv4 host, extra = %v", gw.Extra)
	}
	if gw.State != "reachable" {
		t.Errorf("the best state across addresses wins, got %q", gw.State)
	}
	if gw.Shared {
		t.Error("one device over two families is not a shared MAC")
	}

	const shared = `[{"dst":"10.0.0.119","dev":"eth0","lladdr":"a8:54:b2:00:00:05","state":["REACHABLE"]},
{"dst":"10.0.0.140","dev":"eth0","lladdr":"a8:54:b2:00:00:05","state":["STALE"]},
{"dst":"10.0.0.159","dev":"eth0","lladdr":"a8:54:b2:00:00:05","state":["REACHABLE"]}]`
	hs := Fold(ParseNeigh([]byte(shared)))
	if len(hs) != 3 {
		t.Fatalf("three addresses behind one MAC must stay three rows, got %d", len(hs))
	}
	for _, h := range hs {
		if !h.Shared {
			t.Errorf("%s should be marked as sharing its MAC", h.IP)
		}
	}
}

func TestFoldDedupesSameAddressFromTwoNICs(t *testing.T) {
	const twoNICs = `[{"dst":"10.0.0.1","dev":"eth0","lladdr":"b4:75:0e:00:00:01","state":["STALE"]},
{"dst":"10.0.0.1","dev":"eth1","lladdr":"b4:75:0e:00:00:01","state":["REACHABLE"]}]`
	hs := Fold(ParseNeigh([]byte(twoNICs)))
	if len(hs) != 1 {
		t.Fatalf("one address seen from two NICs is one host, got %d", len(hs))
	}
	if hs[0].State != "reachable" {
		t.Errorf("state = %q, want the better of the two", hs[0].State)
	}
}

func TestRandomizedBit(t *testing.T) {
	for mac, want := range map[string]bool{
		"76:fe:3c:26:46:aa": true,  // 6
		"06:06:2b:de:9f:b2": true,  // 6
		"9a:62:37:f2:e0:2d": true,  // A
		"82:97:10:3f:11:c1": true,  // 2
		"be:00:00:00:00:00": true,  // E
		"b4:75:0e:f7:2c:07": false, // 4
		"a4:77:33:75:f1:10": false, // 4
		"08:92:04:1e:03:89": false, // 8
		"":                  false,
		"zz":                false,
	} {
		if got := Randomized(mac); got != want {
			t.Errorf("Randomized(%q) = %v, want %v", mac, got, want)
		}
	}
}

func TestVendorLookup(t *testing.T) {
	v, _, err := LoadVendors("")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < 5000 {
		t.Fatalf("embedded OUI table has only %d entries", len(v))
	}
	for mac, want := range map[string]string{
		"b4:75:0e:f7:2c:07": "Belkin",
		"a4:77:33:75:f1:10": "Google",
		"10:9c:70:2c:da:c2": "Prusa",
		"44:61:32:59:21:29": "ecobee",
		"94:83:c4:10:71:a7": "GL.iNet",
		"a8:54:b2:6b:05:2f": "Wistron Neweb",
		"76:fe:3c:26:46:aa": "", // randomized, no registry entry
		"ab:cd":             "", // too short to have a prefix
	} {
		if got := v.Lookup(mac); got != want {
			t.Errorf("Lookup(%q) = %q, want %q", mac, got, want)
		}
	}
}

func TestUserNamesOverrideVendor(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `
[names]
"B4:75:0E:00:00:01" = "router"
"10.0.0.7" = "phone"
`)
	v, names, err := LoadVendors(dir)
	if err != nil {
		t.Fatal(err)
	}
	hosts := Fold(ParseNeigh([]byte(neighJSON)))
	Annotate(hosts, []Subnet{{CIDR: "10.0.0.0/24", Iface: "eth0"}}, v, names, "10.0.0.1")
	for _, h := range hosts {
		switch h.IP {
		case "10.0.0.1":
			if h.Name != "router" {
				t.Errorf("name by MAC, case-insensitively: got %q", h.Name)
			}
			if !h.Gateway {
				t.Error("10.0.0.1 is the default gateway")
			}
		case "10.0.0.7":
			if h.Name != "phone" {
				t.Errorf("name by IP: got %q", h.Name)
			}
		case "172.17.0.9":
			if h.Subnet != "" || !h.Virtual {
				t.Errorf("a neighbor outside every link route groups under its iface: subnet %q virtual %v", h.Subnet, h.Virtual)
			}
		}
	}
}

func TestBrokenUserTOMLKeepsVendors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[names\nbroken")
	v, names, err := LoadVendors(dir)
	if err == nil {
		t.Error("a malformed lan.toml should report an error")
	}
	if len(v) < 5000 || names == nil {
		t.Error("the embedded table must survive a broken override")
	}
}

// The regression that started this: an NFS client dials out from a reserved
// port below 1024, so port number alone cannot tell direction.
func TestAttachPeersUsesListenerSetNotPortNumber(t *testing.T) {
	es := []ports.Established{
		{Proto: "tcp", LocalPort: 919, Remote: "10.0.0.119:111", State: "TIME-WAIT"}, // we mounted their NFS
		{Proto: "tcp", LocalPort: 445, Remote: "10.0.0.117:50404", State: "ESTAB"},   // they mounted our SMB
		{Proto: "tcp", LocalPort: 54966, Remote: "10.0.0.104:8009", State: "ESTAB"},  // we cast to them
		{Proto: "tcp", LocalPort: 22, Remote: "10.0.0.117:53906", State: "ESTAB"},    // they ssh'd in
		{Proto: "tcp", LocalPort: 40000, Remote: "not an address", State: "ESTAB"},   // skipped
		{Proto: "tcp6", LocalPort: 445, Remote: "[fe80::1%eth0]:5000", State: "ESTAB"},
	}
	hosts := []Host{
		{IP: "10.0.0.119"}, {IP: "10.0.0.117"}, {IP: "10.0.0.104"},
		{IP: "10.0.0.200", Extra: []string{"fe80::1"}},
	}
	AttachPeers(hosts, es, map[int]bool{22: true, 445: true})

	want := []struct {
		in, out []int
	}{
		{nil, []int{111}},
		{[]int{22, 445}, nil},
		{nil, []int{8009}},
		{[]int{445}, nil}, // matched through the link-local in Extra
	}
	for i, w := range want {
		if !equal(hosts[i].In, w.in) || !equal(hosts[i].Out, w.out) {
			t.Errorf("%s: in=%v out=%v, want in=%v out=%v", hosts[i].IP, hosts[i].In, hosts[i].Out, w.in, w.out)
		}
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "lan.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func countRandom(hs []Host) int {
	n := 0
	for _, h := range hs {
		if h.Random {
			n++
		}
	}
	return n
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListeningPortsAndSubnets(t *testing.T) {
	set := ListeningPorts([]byte("tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*\ntcp LISTEN 0 511 0.0.0.0:445 0.0.0.0:*\ngarbage\n"))
	if !set[22] || !set[445] || set[919] {
		t.Errorf("listening set = %v", set)
	}
	subs := ParseLinkSubnets([]byte(`[{"dst":"default","gateway":"10.0.0.1","dev":"eth0"},
{"dst":"10.0.0.0/24","dev":"eth0","scope":"link"},
{"dst":"172.17.0.0/16","dev":"docker0","scope":"link"},
{"dst":"nonsense/x","dev":"eth0","scope":"link"}]`))
	if len(subs) != 2 {
		t.Fatalf("got %v, want the two parsable link routes", subs)
	}
	if ParseLinkSubnets([]byte("junk")) != nil {
		t.Error("unparsable route output yields no subnets")
	}
}

func TestVirtualIface(t *testing.T) {
	for name, want := range map[string]bool{
		"docker0": true, "br-11d3": true, "veth9a1": true, "wg0": true,
		"tailscale0": true, "virbr0": true,
		"eth0": false, "enp3s0f1": false, "wlan0": false, "bond0": false,
	} {
		if got := VirtualIface(name); got != want {
			t.Errorf("VirtualIface(%q) = %v, want %v", name, got, want)
		}
	}
}

// --demo drives the real parsers over the committed fixtures, so this is the
// render path's integration test too (HANDOFF §9).
func TestFixturesRenderEveryView(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ld := d.(Data)
	if len(ld.Hosts) < 10 {
		t.Fatalf("fixture yielded only %d hosts", len(ld.Hosts))
	}
	if ld.Reachable == 0 || ld.Random == 0 {
		t.Errorf("fixture should exercise both counters: reachable=%d random=%d", ld.Reachable, ld.Random)
	}
	var talks, virtualRandom int
	for _, h := range ld.Hosts {
		if h.Virtual && h.Random {
			virtualRandom++
		}
		talks += len(h.In) + len(h.Out)
	}
	if virtualRandom == 0 {
		t.Error("fixture should contain a container, whose MAC is locally administered")
	}
	// The headline count means "phones rotating their MAC", so the synthetic
	// MACs the kernel hands containers must not be in it.
	if want := countRandom(ld.Hosts) - virtualRandom; ld.Random != want {
		t.Errorf("Random = %d, want %d with virtual interfaces excluded", ld.Random, want)
	}
	if talks == 0 {
		t.Error("fixture should show at least one host this box talks to")
	}
	if lines := strings.Split(m.Card(d, 40), "\n"); len(lines) > 6 {
		t.Errorf("card is %d lines, the limit is 6", len(lines))
	}
	for _, w := range []int{80, 100, 170} {
		for _, h := range []int{0, 12, 30} {
			out := m.View(d, w, h)
			if out == "" {
				t.Fatalf("empty view at %dx%d", w, h)
			}
			if h > 0 && strings.Count(out, "\n")+1 > h {
				t.Errorf("view at %dx%d overflowed to %d lines", w, h, strings.Count(out, "\n")+1)
			}
		}
	}
	if m.Card(nil, 40) == "" || m.View(nil, 80, 10) == "" {
		t.Error("nil data must render a skeleton, not an empty string")
	}
	if m.View(Data{}, 80, 10) == "" {
		t.Error("an empty table must say so")
	}
}

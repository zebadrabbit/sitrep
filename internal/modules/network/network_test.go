package network

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zebadrabbit/sitrep/internal/collect"
)

func TestParsers(t *testing.T) {
	addr := `[{"ifname":"lo","operstate":"UNKNOWN","link_type":"loopback","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},
{"ifname":"enp3s0f0","operstate":"UP","link_type":"ether","address":"aa:bb","mtu":1500,"addr_info":[{"family":"inet","local":"10.0.0.5","prefixlen":24,"scope":"global"},{"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link"}]},
{"ifname":"wg0","operstate":"UNKNOWN","link_type":"none","linkinfo":{"info_kind":"wireguard"},"addr_info":[{"family":"inet","local":"10.0.1.1","prefixlen":24,"scope":"global"}]},
{"ifname":"tailscale0","operstate":"UNKNOWN","link_type":"none","linkinfo":{"info_kind":"tun"},"addr_info":[]}]`
	ifs, err := ParseAddr([]byte(addr))
	if err != nil || len(ifs) != 4 {
		t.Fatalf("ifs=%+v err=%v", ifs, err)
	}
	if ifs[1].Addrs[0] != "10.0.0.5/24" || len(ifs[1].Addrs) != 1 {
		t.Errorf("link-local should be dropped: %+v", ifs[1].Addrs)
	}
	if ifs[2].Kind != "wireguard" || ifs[3].Kind != "tailscale" || ifs[0].Kind != "loopback" {
		t.Errorf("kinds: %s %s %s", ifs[0].Kind, ifs[2].Kind, ifs[3].Kind)
	}
	if _, err := ParseAddr([]byte("nope")); err == nil {
		t.Error("malformed must error")
	}
	gw, dev, _ := ParseRoute([]byte(`[{"dst":"default","gateway":"10.0.0.1","dev":"enp3s0f1","metric":0},{"dst":"default","gateway":"10.0.0.1","dev":"enp3s0f0","metric":100},{"dst":"10.0.0.0/24","dev":"enp3s0f0"}]`))
	if gw != "10.0.0.1" || dev != "enp3s0f1" {
		t.Errorf("route: %s %s", gw, dev)
	}
	nd := ParseNetDev([]byte("Inter-|   Receive\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets\n    lo: 100 1 0 0 0 0 0 0 200 2 0 0 0 0 0 0\nbad line\n"))
	if nd["lo"] != [2]uint64{100, 200} {
		t.Errorf("netdev: %v", nd)
	}
	if dns := ParseResolv([]byte("# c\nnameserver 10.0.0.1\nnameserver 10.0.0.1\nnameserver ::1\n")); len(dns) != 2 {
		t.Errorf("dns: %v", dns)
	}
}

func TestRates(t *testing.T) {
	m := New(true)
	d := Data{Ifaces: []Iface{{Name: "eth0"}}, DefaultIf: "eth0", Collected: time.Unix(100, 0)}
	m.rates(map[string][2]uint64{"eth0": {1000, 500}}, &d)
	d2 := Data{Ifaces: []Iface{{Name: "eth0"}}, DefaultIf: "eth0", Collected: time.Unix(102, 0)}
	m.rates(map[string][2]uint64{"eth0": {3000, 500}}, &d2)
	if d2.Ifaces[0].RxRate != 1000 || d2.Ifaces[0].TxRate != 0 || len(m.rx) != 1 {
		t.Errorf("rate = %+v hist=%v", d2.Ifaces[0], m.rx)
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no network fixture yet:", err)
	}
	nd := d.(Data)
	if nd.DefaultIf == "" || nd.PrimaryIP == "" || len(nd.DNS) == 0 {
		t.Errorf("fixture thin: %+v", nd)
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 28); !strings.Contains(v, "IFACE") || strings.Count(v, "\n") > 27 {
		t.Errorf("view shape: %d lines", strings.Count(v, "\n"))
	}
}

func TestNoIPFallback(t *testing.T) {
	m := New(true)
	m.run = collect.New("network", true).WithFixtures(fstest.MapFS{
		"testdata/fixtures/network/fs/proc/net/dev": {Data: []byte("h\nh\n    lo: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n  eth0: 5 1 0 0 0 0 0 0 5 1 0 0 0 0 0 0\n")},
	})
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nd := d.(Data)
	if !nd.Fallback || len(nd.Ifaces) != 2 || nd.Ifaces[0].Name != "eth0" || nd.Ifaces[0].RxBytes != 5 {
		t.Errorf("fallback: %+v", nd)
	}
	if v := m.View(d, 100, 0); !strings.Contains(v, "ip missing") {
		t.Error("view should say ip is missing")
	}
}

package docker

import (
	"context"
	"strings"
	"testing"
)

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no docker fixture yet:", err)
	}
	dd := d.(Data)
	if dd.Running == 0 {
		t.Fatalf("fixture has no running containers: %+v", dd)
	}
	if _, ok := m.LookupPort("tcp", 3000); !ok {
		t.Error("expected planka on host port 3000 in the fixture")
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Errorf("card too tall:\n%s", c)
	}
	if v := m.View(d, 120, 0); !strings.Contains(v, "NAME") {
		t.Error("view missing header")
	}
}

func TestLookups(t *testing.T) {
	m := New(true)
	m.last = Data{Containers: []Container{{ID: "932613810e80abcdef", Name: "ha", Image: "x", Ports: []PortMap{{HostPort: 3000, Proto: "tcp"}}}}}
	if r, ok := m.LookupPort("tcp6", 3000); !ok || r.Name != "ha" {
		t.Error("tcp6 should match a tcp publish")
	}
	if _, ok := m.LookupPort("udp", 3000); ok {
		t.Error("proto must match")
	}
	if r, ok := m.LookupID("932613810e80abcdef0123456789"); !ok || r.Name != "ha" {
		t.Error("full cgroup id should prefix-match the short id")
	}
}

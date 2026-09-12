package docker

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zebadrabbit/sitrep/internal/collect"
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

func TestParseCPUStat(t *testing.T) {
	if got := ParseCPUStat([]byte("usage_usec 71839923\nuser_usec 49584973\nsystem_usec 22254950\n")); got != 71839923 {
		t.Errorf("got %d", got)
	}
	if ParseCPUStat([]byte("garbage")) != 0 {
		t.Error("missing key should be 0")
	}
}

// Two ticks against a fake cgroup tree: the first has memory but no CPU%,
// the second has both.
func TestStatsTwoTicks(t *testing.T) {
	fsys := fstest.MapFS{
		"testdata/fixtures/docker/fs/sys/fs/cgroup/system.slice/docker-abc123full.scope/cpu.stat":       {Data: []byte("usage_usec 1000000\n")},
		"testdata/fixtures/docker/fs/sys/fs/cgroup/system.slice/docker-abc123full.scope/memory.current": {Data: []byte("52428800\n")},
		"testdata/fixtures/docker/fs/sys/fs/cgroup/system.slice/docker-abc123full.scope/memory.max":     {Data: []byte("max\n")},
	}
	m := New(true)
	m.run = collect.New("docker", true).WithFixtures(fsys)
	cs := []Container{{ID: "abc123", Name: "web", Running: true}, {ID: "dead", Name: "old", Running: false}}
	t0 := time.Now()
	m.stats(cs, t0)
	if cs[0].HasCPU || !cs[0].HasMem || cs[0].Mem != 52428800 || cs[0].MemLimit != 0 {
		t.Errorf("first tick: %+v", cs[0])
	}
	fsys["testdata/fixtures/docker/fs/sys/fs/cgroup/system.slice/docker-abc123full.scope/cpu.stat"] = &fstest.MapFile{Data: []byte("usage_usec 2500000\n")}
	m.stats(cs, t0.Add(5*time.Second))
	if !cs[0].HasCPU || cs[0].CPUPct < 29.9 || cs[0].CPUPct > 30.1 {
		t.Errorf("second tick: %+v", cs[0])
	}
	if cs[1].HasMem {
		t.Error("stopped container should have no stats")
	}
	v := m.View(Data{Containers: cs, Runtime: "docker"}, 120, 0)
	if !strings.Contains(v, "30.0%") || !strings.Contains(v, "50.0M") {
		t.Errorf("view:\n%s", v)
	}
}

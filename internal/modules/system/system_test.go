package system

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sitrep "github.com/zebadrabbit/sitrep"
)

func TestDemoFixtureRenders(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sd := d.(Data)
	if sd.MemTotal == 0 || sd.Kernel == "" {
		t.Errorf("fixture looks empty: %+v", sd)
	}
	if card := m.Card(d, 60); strings.Count(card, "\n") > 5 {
		t.Errorf("card exceeds 6 lines:\n%s", card)
	}
	if v := m.View(d, 100, 28); !strings.Contains(v, "top by cpu") || !strings.Contains(v, "node0") {
		t.Errorf("view missing sections")
	}
	if _, err := json.Marshal(d); err != nil {
		t.Error(err)
	}
}

func TestCardHandlesNoData(t *testing.T) {
	if c := New(true).Card(nil, 60); !strings.Contains(c, "collecting") {
		t.Errorf("got %q", c)
	}
}

func TestParsePressure(t *testing.T) {
	in := []byte("some avg10=0.18 avg60=0.16 avg300=0.81 total=13856151\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	if got := ParsePressure(in); got != 0.18 {
		t.Errorf("got %v want 0.18", got)
	}
	if got := ParsePressure(nil); got != 0 {
		t.Errorf("missing file: got %v want 0", got)
	}
}

func TestParseLscpu(t *testing.T) {
	b, err := sitrep.Fixtures.ReadFile("testdata/fixtures/system/lscpu_J.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseLscpu(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "Intel Xeon E5504 @ 2.00GHz" {
		t.Errorf("model %q", got.Model)
	}
	if got.Sockets != 2 || got.Cores != 4 || got.Threads != 1 || got.MaxMHz != 2000 || got.MinMHz != 1600 {
		t.Errorf("topology %+v", got)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].List != "0,2,4,6" || !reflect.DeepEqual(got.Nodes[1].CPUs, []int{1, 3, 5, 7}) {
		t.Errorf("nodes %+v", got.Nodes)
	}
	if _, err := ParseLscpu([]byte("nope")); err == nil {
		t.Error("garbage should fail")
	}
}

func TestParseCPUList(t *testing.T) {
	if got := parseCPUList("0-3,8,10-11"); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 8, 10, 11}) {
		t.Errorf("got %v", got)
	}
}

func TestParseNodeMeminfo(t *testing.T) {
	in := []byte("Node 0 MemTotal:       37080724 kB\nNode 0 MemFree:        31952208 kB\nNode 0 MemUsed:         5128516 kB\n")
	total, used := ParseNodeMeminfo(in)
	if total != 37080724*1024 || used != 5128516*1024 {
		t.Errorf("got %d %d", total, used)
	}
}

// cpuBlock picks the htop grid when it fits the height and one cell per cpu
// when it does not; both group by NUMA node.
func TestCPUBlockLayouts(t *testing.T) {
	small := Data{CPUs: 8, PerCPU: make([]float64, 8), Topo: Topology{Nodes: []Node{
		{ID: 0, CPUs: []int{0, 2, 4, 6}, List: "0,2,4,6"}, {ID: 1, CPUs: []int{1, 3, 5, 7}, List: "1,3,5,7"},
	}}}
	grid := cpuBlock(small, 100, 10)
	if !strings.Contains(grid, "node0") || !strings.Contains(grid, "░") || strings.Count(grid, "\n") > 4 {
		t.Errorf("grid:\n%s", grid)
	}
	big := Data{CPUs: 256, PerCPU: make([]float64, 256)}
	for i := range 4 {
		n := Node{ID: i}
		for c := i * 64; c < (i+1)*64; c++ {
			n.CPUs = append(n.CPUs, c)
		}
		big.Topo.Nodes = append(big.Topo.Nodes, n)
	}
	strip := cpuBlock(big, 100, 10)
	if strings.Contains(strip, "░") || strings.Count(strip, "\n") != 3 {
		t.Errorf("strip should be one line per node:\n%s", strip)
	}
	// No topology at all: one flat block, no node header.
	flat := cpuBlock(Data{CPUs: 4, PerCPU: make([]float64, 4)}, 100, 10)
	if strings.Contains(flat, "node") || !strings.Contains(flat, "░") {
		t.Errorf("flat:\n%s", flat)
	}
}

func TestReadSensors(t *testing.T) {
	root := t.TempDir()
	w := func(p, v string) {
		p = filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("hwmon10/name", "coretemp") // sorts after hwmon2 numerically, not lexically
	w("hwmon10/temp2_input", "44000")
	w("hwmon10/temp2_label", "Core 0")
	w("hwmon10/temp2_max", "87000")
	w("hwmon10/temp2_crit", "97000")
	w("hwmon10/temp10_input", "50000")
	w("hwmon2/name", "acpitz")
	w("hwmon2/temp1_input", "8300")
	w("hwmon2/fan1_input", "1200")
	w("hwmon2/fan2_input", "0")     // stopped or absent header: dropped
	w("hwmon3/temp1_input", "1000") // no name: skipped
	temps, fans := readSensors(root)
	if len(temps) != 3 || temps[0].Chip != "acpitz" || temps[0].Value != 8.3 {
		t.Fatalf("temps %+v", temps)
	}
	if temps[1].Label != "Core 0" || temps[1].Max != 87 || temps[1].Crit != 97 || temps[2].Value != 50 {
		t.Errorf("coretemp %+v", temps[1:])
	}
	if temps[0].Warn() != 80 || temps[0].CritAt() != 95 || temps[1].Warn() != 87 {
		t.Error("threshold fallbacks")
	}
	if len(fans) != 1 || fans[0].Value != 1200 {
		t.Errorf("fans %+v", fans)
	}
	if temps, fans := readSensors(filepath.Join(root, "nope")); temps != nil || fans != nil {
		t.Error("missing tree should be empty")
	}
}

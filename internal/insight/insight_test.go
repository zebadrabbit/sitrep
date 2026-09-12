package insight

import (
	"testing"

	"github.com/zebadrabbit/sitrep/internal/modules/disks"
	"github.com/zebadrabbit/sitrep/internal/modules/services"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
)

func TestCheck(t *testing.T) {
	data := map[string]any{
		"system": system.Data{MemUsed: 96, MemTotal: 100, SwapUsed: 6, SwapTotal: 10, Load: [3]float64{9, 1, 1}, CPUs: 4,
			Temps: []system.Sensor{{Chip: "coretemp", Label: "Core 0", Value: 70, Max: 87}, {Chip: "acpitz", Value: 82}}},
		"disks": disks.Data{Mounts: []disks.Mount{{Target: "/", Pct: 50}, {Target: "/data", Pct: 96}, {Target: "/opt", Pct: 88}}},
		"services": services.Data{Failed: 2, Units: []services.Unit{
			{Unit: "a.service", Active: "failed"}, {Unit: "b.service", Active: "active"}, {Unit: "c.service", Active: "failed"}}},
		"docker": map[string]string{"error": "docker: not running"}, // snapshot's error shape is skipped
	}
	got := Check(data)
	want := []Insight{
		{Crit, "System", "memory 96% used and swap 60%: thrashing likely"},
		{Crit, "Disks", "/data is 96% full"},
		{Warn, "System", "load 9.0 on 4 cpus"},
		{Warn, "System", "acpitz at 82°C (max 80)"},
		{Warn, "Disks", "/opt is 88% full"},
		{Warn, "Services", "2 units failed: a, c"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d insights, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("insight %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if n := len(Check(map[string]any{})); n != 0 {
		t.Errorf("empty store gave %d insights", n)
	}
}

package disks

import (
	"context"
	"strings"
	"testing"
)

func TestParsers(t *testing.T) {
	ls := `{"blockdevices":[{"name":"sda","size":"223.6G","type":"disk","fstype":null,"mountpoints":[null],"model":"Samsung SSD","children":[{"name":"sda2","size":"222G","type":"part","fstype":"ext4","mountpoints":["/"],"model":null}]},{"name":"loop0","size":"63M","type":"loop","fstype":"squashfs","mountpoints":["/snap/x"],"model":null}]}`
	devs, err := ParseLsblk([]byte(ls))
	if err != nil || len(devs) != 2 || devs[0].Children[0].Mountpoints[0] != "/" {
		t.Fatalf("devs=%+v err=%v", devs, err)
	}
	if _, err := ParseLsblk([]byte("{nope")); err == nil {
		t.Error("malformed must error")
	}
	if st, _ := ParseSmart([]byte(`{"smart_status":{"passed":true}}`)); st != "PASSED" {
		t.Error("smart passed")
	}
	if st, _ := ParseSmart([]byte(`{"smart_status":{"passed":false}}`)); st != "FAILED" {
		t.Error("smart failed")
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no disks fixture yet:", err)
	}
	dd := d.(Data)
	if dd.NDevices == 0 || len(dd.Mounts) == 0 || dd.Worst == nil {
		t.Fatalf("fixture thin: %+v", dd)
	}
	for _, dev := range dd.Devices {
		if dev.Type == "loop" {
			t.Error("loop devices must be dropped")
		}
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 0); !strings.Contains(v, "block devices") {
		t.Error("view missing tree")
	}
}

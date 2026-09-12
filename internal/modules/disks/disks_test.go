package disks

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestParseMdstat(t *testing.T) {
	in := []byte(`Personalities : [raid1] [raid6] [raid5] [raid4]
md1 : active raid5 sdd1[3] sdc1[2] sdb1[1] sda1[0](F)
      5860147200 blocks super 1.2 level 5, 512k chunk, algorithm 2 [4/3] [_UUU]
      [==>..................]  recovery = 12.3% (240000000/1953382400) finish=180.2min speed=158000K/sec

md0 : active raid1 sdb2[1] sda2[0]
      1953382400 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`)
	a := ParseMdstat(in)
	if len(a) != 2 {
		t.Fatalf("got %+v", a)
	}
	if a[0].Name != "md1" || a[0].Level != "raid5" || !a[0].Degraded || a[0].Status != "[_UUU]" || a[0].Progress != "recovery 12.3%" || len(a[0].Members) != 4 || a[0].Members[3] != "sda1" {
		t.Errorf("md1: %+v", a[0])
	}
	if a[1].Level != "raid1" || a[1].Degraded || a[1].Status != "[UU]" || a[1].Progress != "" {
		t.Errorf("md0: %+v", a[1])
	}
	if len(ParseMdstat([]byte("Personalities : [raid1]\nunused devices: <none>\n"))) != 0 {
		t.Error("no arrays")
	}
}

func TestParseZpoolList(t *testing.T) {
	p := ParseZpoolList([]byte("tank\tONLINE\t3.62T\t1.20T\t33%\nbackup\tDEGRADED\t1.81T\t900G\t48%\n"))
	if len(p) != 2 || p[0].Name != "tank" || p[0].Cap != "33%" || p[1].Health != "DEGRADED" {
		t.Errorf("got %+v", p)
	}
}

func TestDiskRates(t *testing.T) {
	prev := ParseDiskstats([]byte("   8 0 sda 100 0 2000 50 200 0 4000 80 0 1000 130 0 0 0 0 0 0\n   8 1 sda1 100 0 2000 50 200 0 4000 80 0 1000 130 0 0 0 0 0 0\n 253 0 dm-0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n   7 0 loop0 1 0 8 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"))
	cur := ParseDiskstats([]byte("   8 0 sda 150 0 4048 60 300 0 8096 90 0 6000 150 0 0 0 0 0 0\n   8 1 sda1 150 0 4048 60 300 0 8096 90 0 6000 150 0 0 0 0 0 0\n 253 0 dm-0 0 0 1024 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"))
	if _, ok := prev["loop0"]; ok {
		t.Error("loops should be dropped")
	}
	devs := []Device{{Name: "sda", KName: "sda", Type: "disk", Children: []Device{{Name: "sda1", KName: "sda1", Type: "part"}, {Name: "vg-lv", KName: "dm-0", Type: "lvm"}}}}
	rates(devs, prev, cur, 10*time.Second)
	d := devs[0]
	if !d.HasIO || d.RdRate != 2048*512/10.0 || d.WrRate != 4096*512/10.0 || d.Util != 50 {
		t.Errorf("got %+v", d)
	}
	if devs[0].Children[0].HasIO {
		t.Error("partitions are skipped even with counters")
	}
	if lv := devs[0].Children[1]; !lv.HasIO || lv.RdRate != 1024*512/10.0 {
		t.Errorf("lvm matched by kernel name: %+v", lv)
	}
}

func TestRaidView(t *testing.T) {
	d := Data{
		Arrays: []Array{{Name: "md0", Level: "raid1", State: "active", Status: "[U_]", Degraded: true, Members: []string{"sda1", "sdb1"}, Progress: "recovery 12.3%"}},
		Pools:  []Pool{{Name: "tank", Health: "ONLINE", Size: "3.6T", Alloc: "1.2T", Cap: "33%"}},
	}
	v := New(true).View(d, 100, 0)
	for _, want := range []string{"md0", "raid1", "[U_]", "recovery 12.3%", "tank", "ONLINE", "33%"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(New(true).Card(d, 60), "md0") {
		t.Error("card should name the degraded array")
	}
}

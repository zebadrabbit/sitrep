package nfs

import (
	"context"
	"strings"
	"testing"
)

func TestParseExports(t *testing.T) {
	out := `/srv/media    	10.0.0.0/24(rw,wdelay,root_squash,no_subtree_check,sec=sys,rw,secure,root_squash,no_all_squash)
/srv/a/very/long/path/that/wraps/onto/the/next/line
		10.0.0.7(ro,wdelay,root_squash,no_subtree_check,sec=sys,ro,secure,root_squash,no_all_squash)
/srv/world    	<world>(ro,sync)
`
	ex := ParseExports([]byte(out))
	if len(ex) != 3 {
		t.Fatalf("got %d exports: %+v", len(ex), ex)
	}
	if ex[0].Client != "10.0.0.0/24" || !strings.HasPrefix(ex[0].Options, "rw,") {
		t.Errorf("first: %+v", ex[0])
	}
	if ex[1].Path != "/srv/a/very/long/path/that/wraps/onto/the/next/line" || ex[1].Client != "10.0.0.7" {
		t.Errorf("wrapped: %+v", ex[1])
	}
	if ex[2].Client != "<world>" {
		t.Errorf("world: %+v", ex[2])
	}
}

func TestParseMounts(t *testing.T) {
	out := "/dev/sda2 / ext4 rw 0 0\n10.0.0.5:/srv/media /mnt/media nfs4 rw,vers=4.2 0 0\nbad\n"
	ms := ParseMounts([]byte(out))
	if len(ms) != 1 || ms[0].Target != "/mnt/media" || ms[0].FSType != "nfs4" {
		t.Errorf("mounts: %+v", ms)
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no nfs fixture:", err)
	}
	nd := d.(Data)
	if !nd.Server || len(nd.Exports) == 0 {
		t.Errorf("fixture thin: %+v", nd)
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 0); !strings.Contains(v, "exports") {
		t.Error("view")
	}
}

package ports

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zebadrabbit/sitrep/internal/collect"
)

// synthetic builds a demo runner over an in-memory fixture set.
func synthetic(t *testing.T, files map[string]string) *Module {
	t.Helper()
	fsys := fstest.MapFS{}
	for k, v := range files {
		fsys["testdata/fixtures/ports/"+k] = &fstest.MapFile{Data: []byte(v)}
	}
	m := New(true)
	m.run = collect.New("ports", true).WithFixtures(fsys)
	return m
}

const procStat = "cpu  1 2 3 4\nbtime 1700000000\n"

func TestCollectSyntheticEdgeCases(t *testing.T) {
	m := synthetic(t, map[string]string{
		"ss_tulnpH.txt": `tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=42,fd=3))
tcp LISTEN 0 4096 0.0.0.0:3000 0.0.0.0:* users:(("docker-proxy",pid=77,fd=7))
tcp LISTEN 0 128 127.0.0.1:8787 0.0.0.0:* users:(("python3",pid=99,fd=8))
tcp LISTEN 0 4096 [::]:8096 [::]:*
udp UNCONN 0 0 0.0.0.0:5353 0.0.0.0:*
tcp LISTEN 0 4096 0.0.0.0:9998 0.0.0.0:* users:(("mystery",pid=555,fd=1))
this is a malformed line
`,
		"ss_tunaH.txt": `tcp ESTAB 0 0 10.0.0.5:22 10.0.0.9:50103
tcp ESTAB 0 0 10.0.0.5:22 10.0.0.9:50104
tcp ESTAB 0 0 10.0.0.5:3000 10.0.0.9:1
`,
		"fs/proc/stat":       procStat,
		"fs/proc/uptime":     "3600.00 1000.00\n",
		"fs/proc/42/stat":    "42 (sshd) S 1 42 42 0 -1 4194560 100 0 0 0 0 0 0 0 20 0 1 0 6000 1000 1 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		"fs/proc/42/status":  "Name:\tsshd\nUid:\t0\t0\t0\t0\nPPid:\t1\nThreads:\t1\n",
		"fs/proc/42/cmdline": "sshd: /usr/sbin/sshd -D\x00",
		"fs/proc/42/cgroup":  "0::/system.slice/ssh.service\n",
		"fs/proc/77/stat":    "77 (docker-proxy) S 1 77 77 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 300000 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		"fs/proc/77/status":  "Uid:\t0\t0\t0\t0\nPPid:\t1\nThreads:\t4\n",
		"fs/proc/99/stat":    "99 (python3) S 1 99 99 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 359000 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		"fs/proc/99/status":  "Uid:\t1000\t1000\t1000\t1000\nPPid:\t1\nThreads:\t2\n",
		// pid 555 has no /proc files: the unreadable (needs-root) case.
	})
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pd := d.(Data)
	if pd.Listening != 6 {
		t.Fatalf("listening = %d, want 6 (malformed line skipped)", pd.Listening)
	}
	by := map[int]Row{}
	for _, r := range pd.Rows {
		by[r.Port] = r
	}
	ssh := by[22]
	if ssh.Identity.Name != "ssh" || ssh.Identity.Source != SrcProcess || ssh.Conn != 2 || ssh.Detail.Unit != "ssh.service" {
		t.Errorf("ssh row: %+v", ssh)
	}
	if ssh.Age != 3600*time.Second-60*time.Second {
		t.Errorf("ssh age = %v, want 59m (uptime 3600s − start at 60s)", ssh.Age)
	}
	if got := ssh.Detail.Cmdline; !strings.HasPrefix(got, "sshd:") {
		t.Errorf("cmdline = %q", got)
	}
	if dp := by[3000]; dp.Identity.Source != SrcDocker || dp.Conn != 1 {
		t.Errorf("docker-proxy row: %+v", dp)
	}
	if lo := by[8787]; !lo.Loopback || lo.User != "" && lo.User != "1000" && !strings.Contains(lo.Identity.Name, "dev") {
		t.Errorf("loopback dev row: %+v", lo)
	}
	if v6 := by[8096]; v6.Proto != "tcp6" || v6.Identity.Name != "jellyfin" || v6.PID != 0 || !v6.Since.IsZero() {
		t.Errorf("ipv6 needs-root row: %+v", v6)
	}
	if u := by[5353]; !strings.HasPrefix(u.Proto, "udp") || u.Identity.Name != "mdns" {
		t.Errorf("udp row: %+v", u)
	}
	if my := by[9998]; !my.Since.IsZero() || my.Identity.Source != SrcHeuristic {
		t.Errorf("unreadable pid row should have no age and heuristic identity: %+v", my)
	}
	for _, r := range pd.Rows {
		if r.New {
			t.Errorf("first collection must not mark rows new: %+v", r)
		}
	}
	if pd.Unknown != 1 || pd.Sources[SrcProcess] != 1 || pd.Sources[SrcPortTable] != 3 {
		t.Errorf("sources = %v unknown = %d", pd.Sources, pd.Unknown)
	}
}

func TestNewAndGoneMarkers(t *testing.T) {
	base := map[string]string{
		"fs/proc/stat": procStat, "fs/proc/uptime": "100.00 1.00\n",
		"ss_tunaH.txt": "",
	}
	m := synthetic(t, base)
	m.started = time.Unix(1700000000, 0) // before the fixture "now"
	first := "tcp LISTEN 0 1 0.0.0.0:22 0.0.0.0:*\n"
	base["ss_tulnpH.txt"] = first
	m = synthetic(t, base)
	m.started = time.Unix(1700000000, 0)
	if _, err := m.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second collection: 22 gone, 8080 new.
	base["ss_tulnpH.txt"] = "tcp LISTEN 0 1 0.0.0.0:8080 0.0.0.0:*\n"
	m2 := synthetic(t, base)
	m2.started, m2.seen, m2.prev = m.started, m.seen, m.prev
	d, err := m2.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sawNew, sawGone bool
	for _, r := range d.(Data).Rows {
		sawNew = sawNew || (r.Port == 8080 && r.New)
		sawGone = sawGone || (r.Port == 22 && r.Gone)
	}
	if !sawNew || !sawGone {
		t.Errorf("new=%v gone=%v rows=%+v", sawNew, sawGone, d.(Data).Rows)
	}
}

func TestMissingEverything(t *testing.T) {
	m := synthetic(t, map[string]string{})
	if _, err := m.Collect(context.Background()); err == nil {
		t.Fatal("expected error when neither ss nor /proc/net fixtures exist")
	}
}

func TestEmbeddedFixtureRendersAllViews(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pd := d.(Data)
	if pd.Listening < 10 {
		t.Fatalf("embedded fixture has only %d listeners", pd.Listening)
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Errorf("card too tall:\n%s", c)
	}
	list := m.View(d, 100, 28)
	if !strings.Contains(list, "IDENTITY") || strings.Count(list, "\n") > 27 {
		t.Errorf("list view wrong shape (%d lines)", strings.Count(list, "\n"))
	}
	m.ui.detail = true
	if det := m.View(d, 100, 28); !strings.Contains(det, "identity") || !strings.Contains(det, "probe") {
		t.Errorf("detail view missing sections:\n%s", det)
	}
}

func TestScriptPath(t *testing.T) {
	cases := []struct{ cmd, cwd, want string }{
		{"/usr/bin/python3 app.py --port 8000", "/srv/app", "/srv/app/app.py"},
		{"/usr/bin/python3 -P -m homeassistant --config /config", "/", ""},
		{"node /opt/bot/index.js", "/tmp", "/opt/bot/index.js"},
		{"python3", "", ""},
	}
	for _, c := range cases {
		if got := scriptPath(c.cmd, c.cwd); got != c.want {
			t.Errorf("scriptPath(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestStaleDetection(t *testing.T) {
	m := synthetic(t, map[string]string{
		"ss_tulnpH.txt": `tcp LISTEN 0 1 0.0.0.0:8000 0.0.0.0:* users:(("python3",pid=5,fd=3))
tcp LISTEN 0 1 0.0.0.0:9000 0.0.0.0:* users:(("nginx",pid=6,fd=3))
`,
		"ss_tunaH.txt":   "",
		"fs/proc/stat":   procStat,
		"fs/proc/uptime": "3600.00 1.00\n",
		// python3 started at t+60s; app.py modified at t+120s → stale.
		"fs/proc/5/stat":                      "5 (python3) S 1 5 5 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 6000 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		"fs/proc/5/status":                    "Uid:\t1000\nPPid:\t1\nThreads:\t1\n",
		"fs/proc/5/cmdline":                   "/usr/bin/python3\x00app.py\x00",
		"fs/proc/5/cwd.link":                  "/srv/app",
		"fs/proc/5/exe.link":                  "/usr/bin/python3.12",
		"fs/proc/5/root/srv/app/app.py.mtime": "2023-11-14T22:15:20Z",
		"fs/proc/6/stat":                      "6 (nginx) S 1 6 6 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 6000 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		"fs/proc/6/status":                    "Uid:\t0\nPPid:\t1\nThreads:\t1\n",
		"fs/proc/6/exe.link":                  "/usr/sbin/nginx (deleted)",
	})
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range d.(Data).Rows {
		switch r.Port {
		case 8000:
			if r.Detail.Source != "/srv/app/app.py" || !r.Detail.Stale {
				t.Errorf("script row: %+v", r.Detail)
			}
		case 9000:
			if r.Detail.Source != "/usr/sbin/nginx" || !r.Detail.Stale {
				t.Errorf("deleted-exe row: %+v", r.Detail)
			}
		}
	}
}

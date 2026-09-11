package updates

import "testing"

func TestParseAptList(t *testing.T) {
	out := `Listing...
base-files/noble-updates 13ubuntu10.5 amd64 [upgradable from: 13ubuntu10.4]
libc-bin/noble-updates,noble-security 2.39-0ubuntu8.9 amd64 [upgradable from: 2.39-0ubuntu8.8]
garbage line here
`
	p := ParseAptList([]byte(out))
	if len(p) != 2 || p[0].Name != "base-files" || p[0].To != "13ubuntu10.5" || p[0].From != "13ubuntu10.4" {
		t.Fatalf("got %+v", p)
	}
	if p[0].Security || !p[1].Security {
		t.Errorf("security flag wrong: %+v", p)
	}
	if len(ParseAptList(nil)) != 0 {
		t.Error("empty input should give no packages")
	}
}

func TestParseLog(t *testing.T) {
	log := `===== 2026-09-01T10:00:00-05:00 =====
--- apt ---
!! failed: sudo apt-get -y full-upgrade
--- docker ---
--- summary ---
failures this run:
!! failed: sudo apt-get -y full-upgrade
done 2026-09-01T10:05:00-05:00
===== 2026-09-02T23:28:17-05:00 =====
--- apt ---
0 upgraded
--- snap ---
** REBOOT REQUIRED **
`
	runs := ParseLog([]byte(log))
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	if r := runs[0]; !r.Complete || r.Failures != 1 || len(r.Steps) != 3 || len(r.Steps[0].Failed) != 1 {
		t.Errorf("first run: %+v", r)
	}
	if r := runs[1]; r.Complete || !r.RebootRequired || r.Failures != 0 {
		t.Errorf("second (unfinished) run: %+v", r)
	}
	if len(ParseLog([]byte("no header\n!! failed: x\n"))) != 0 {
		t.Error("lines before any header must be ignored")
	}
}

func TestNewerKernel(t *testing.T) {
	if !newerKernel("6.8.0-139-generic", "6.8.0-138-generic") || newerKernel("6.8.0-138-generic", "6.8.0-139-generic") {
		t.Error("patch compare")
	}
	if !newerKernel("6.9.0-1-generic", "6.8.0-200-generic") {
		t.Error("minor beats patch")
	}
	if kernelVersion("/boot/vmlinuz-6.8.0-139-generic") != "6.8.0-139-generic" {
		t.Error("kernelVersion")
	}
}

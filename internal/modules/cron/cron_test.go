package cron

import (
	"context"
	"strings"
	"testing"
)

func TestParseTimers(t *testing.T) {
	out := `[{"next":1789102800000000,"left":1,"last":1789016400505487,"passed":2,"unit":"dpkg-db-backup.timer","activates":"dpkg-db-backup.service"},
{"next":null,"left":null,"last":0,"passed":0,"unit":"ua-timer.timer","activates":"ua-timer.service"}]`
	ts, err := ParseTimers([]byte(out))
	if err != nil || len(ts) != 2 {
		t.Fatalf("ts=%+v err=%v", ts, err)
	}
	if ts[0].Next.IsZero() || ts[0].Last.IsZero() || !ts[1].Next.IsZero() || !ts[1].Last.IsZero() {
		t.Errorf("timestamps: %+v", ts)
	}
	if ts, err := ParseTimers(nil); err != nil || ts != nil {
		t.Error("empty")
	}
	if _, err := ParseTimers([]byte("[{")); err == nil {
		t.Error("malformed must error")
	}
}

func TestParseShow(t *testing.T) {
	r := ParseShow([]byte("Id=a.service\nResult=success\n\nId=b.service\nResult=exit-code\n"))
	if r["a.service"] != "success" || r["b.service"] != "exit-code" {
		t.Errorf("show: %v", r)
	}
}

func TestParseCrontab(t *testing.T) {
	sys := `# comment
SHELL=/bin/sh
PATH=/usr/bin
17 *	* * *	root	cd / && run-parts --report /etc/cron.hourly
@reboot root /usr/local/bin/x
broken line
`
	jobs := ParseCrontab([]byte(sys), "/etc/crontab", "", true)
	if len(jobs) != 2 || jobs[0].User != "root" || jobs[0].Schedule != "17 * * * *" || !strings.HasPrefix(jobs[0].Command, "cd /") {
		t.Errorf("system: %+v", jobs)
	}
	if jobs[1].Schedule != "@reboot" || jobs[1].Command != "/usr/local/bin/x" {
		t.Errorf("@reboot: %+v", jobs[1])
	}
	user := ParseCrontab([]byte("0 3 * * * /home/u/backup.sh\n"), "crontab:u", "u", false)
	if len(user) != 1 || user[0].User != "u" || user[0].Command != "/home/u/backup.sh" {
		t.Errorf("user: %+v", user)
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no cron fixture:", err)
	}
	cd := d.(Data)
	if len(cd.Timers) == 0 || len(cd.Jobs) == 0 {
		t.Fatalf("fixture thin: %d timers %d jobs", len(cd.Timers), len(cd.Jobs))
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 30); !strings.Contains(v, "TIMER") || strings.Count(v, "\n") > 29 {
		t.Error("view")
	}
}

package logs

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseJournal(t *testing.T) {
	out := `{"__REALTIME_TIMESTAMP":"1789100954118927","PRIORITY":"3","_SYSTEMD_UNIT":"init.scope","UNIT":"gunicorn-x.service","SYSLOG_IDENTIFIER":"systemd","MESSAGE":"Failed to start x"}
{"__REALTIME_TIMESTAMP":"1789100954118999","PRIORITY":"2","SYSLOG_IDENTIFIER":"kernel","MESSAGE":[104,105]}
{"__REALTIME_TIMESTAMP":"1789100954119000","PRIORITY":"3","_SYSTEMD_UNIT":"foo.service","MESSAGE":"bar"}
not json
`
	es := ParseJournal([]byte(out))
	if len(es) != 3 {
		t.Fatalf("got %d", len(es))
	}
	if es[0].Unit != "gunicorn-x" || es[0].Time.Year() < 2026 {
		t.Errorf("first: %+v", es[0])
	}
	if es[1].Unit != "kernel" || es[1].Message != "(binary message)" || es[1].Priority != 2 {
		t.Errorf("binary: %+v", es[1])
	}
	if es[2].Unit != "foo" {
		t.Errorf("suffix: %+v", es[2])
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no logs fixture:", err)
	}
	ld := d.(Data)
	if len(ld.Entries) == 0 {
		t.Fatal("no entries")
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 20); strings.Count(v, "\n") > 19 {
		t.Errorf("view too tall: %d", strings.Count(v, "\n"))
	}
}

func TestCollapse(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	e := func(m time.Duration, unit, msg string) Entry { return Entry{Time: t0.Add(m), Unit: unit, Message: msg} }
	rows := Collapse([]Entry{e(0, "a", "x"), e(time.Minute, "a", "x"), e(2*time.Minute, "b", "x"), e(3*time.Minute, "a", "x")})
	if len(rows) != 3 || rows[0].Count != 2 || rows[1].Count != 1 || rows[2].Count != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	if !rows[0].Time.Equal(t0.Add(time.Minute)) {
		t.Errorf("collapsed row should keep the latest time: %v", rows[0].Time)
	}
}

func TestRates(t *testing.T) {
	now := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	ago := func(d time.Duration, unit string) Entry { return Entry{Time: now.Add(-d), Unit: unit} }
	rs := Rates([]Entry{ago(50*time.Minute, "y"), ago(40*time.Minute, "x"), ago(10*time.Minute, "x"), ago(2*time.Minute, "x"), ago(time.Minute, "x")}, now)
	if len(rs) != 2 || rs[0].Unit != "x" {
		t.Fatalf("sorted by hour count: %+v", rs)
	}
	if x := rs[0]; x.M5 != 2 || x.M15 != 3 || x.H1 != 4 || !x.Building {
		t.Errorf("x: %+v", x)
	}
	if y := rs[1]; y.M5 != 0 || y.M15 != 0 || y.H1 != 1 || y.Building {
		t.Errorf("y: %+v", y)
	}
}

package logs

import (
	"context"
	"strings"
	"testing"
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

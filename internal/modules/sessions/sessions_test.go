package sessions

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseWhoAndMerge(t *testing.T) {
	now := time.Date(2026, 9, 10, 22, 0, 0, 0, time.Local)
	ss := ParseWho([]byte("winter   pts/0        2026-09-10 21:52 (10.0.0.9)\nroot     tty1         2026-09-01 08:00\nbad\n"), now)
	if len(ss) != 2 || ss[0].Remote != "10.0.0.9" || ss[1].Remote != "" || ss[0].Since.Minute() != 52 {
		t.Fatalf("who: %+v", ss)
	}
	merge(ss, ParseLoginctl([]byte(`[{"session":"626","uid":1000,"user":"winter","tty":"pts/0","state":"active","idle":true},{"session":"7","user":"x","tty":null}]`)))
	if ss[0].ID != "626" || !ss[0].Idle || ss[1].ID != "" {
		t.Errorf("merge: %+v", ss)
	}
	if len(ParseLoginctl([]byte("garbage"))) != 0 {
		t.Error("bad json should yield nothing, not panic")
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no sessions fixture:", err)
	}
	sd := d.(Data)
	if sd.Users == 0 {
		t.Fatal("fixture has no users")
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 0); !strings.Contains(v, "USER") {
		t.Error("view")
	}
}

package services

import (
	"context"
	"strings"
	"testing"
)

func TestParseUnits(t *testing.T) {
	out := `[{"unit":"certbot.service","load":"loaded","active":"failed","sub":"failed","description":"Certbot"},
{"unit":"ssh.service","load":"loaded","active":"active","sub":"running","description":"OpenBSD Secure Shell server"}]`
	us, err := ParseUnits([]byte(out))
	if err != nil || len(us) != 2 || us[0].Active != "failed" {
		t.Fatalf("us=%+v err=%v", us, err)
	}
	if us, err := ParseUnits(nil); err != nil || len(us) != 0 {
		t.Error("empty output should be no units, no error")
	}
	if _, err := ParseUnits([]byte("[{broken")); err == nil {
		t.Error("malformed json must be an error")
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no services fixture yet:", err)
	}
	sd := d.(Data)
	if sd.Active == 0 || sd.Units[0].Active != "failed" && sd.Failed > 0 {
		t.Errorf("failed units should sort first: %+v", sd.Units[:2])
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Errorf("card too tall")
	}
	v := m.View(d, 100, 28)
	if !strings.Contains(v, "hidden") || strings.Count(v, "\n") > 27 {
		t.Errorf("view shape wrong")
	}
	m.ui.showAll = true
	if v2 := m.View(d, 100, 0); strings.Count(v2, "\n") <= strings.Count(v, "\n") {
		t.Error("a should reveal inactive units")
	}
}

package system

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDemoFixtureRenders(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sd := d.(Data)
	if sd.MemTotal == 0 || sd.Kernel == "" {
		t.Errorf("fixture looks empty: %+v", sd)
	}
	if card := m.Card(d, 60); strings.Count(card, "\n") > 5 {
		t.Errorf("card exceeds 6 lines:\n%s", card)
	}
	if v := m.View(d, 100, 28); !strings.Contains(v, "top by cpu") {
		t.Errorf("view missing sections")
	}
	if _, err := json.Marshal(d); err != nil {
		t.Error(err)
	}
}

func TestCardHandlesNoData(t *testing.T) {
	if c := New(true).Card(nil, 60); !strings.Contains(c, "collecting") {
		t.Errorf("got %q", c)
	}
}

func TestParsePressure(t *testing.T) {
	in := []byte("some avg10=0.18 avg60=0.16 avg300=0.81 total=13856151\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	if got := ParsePressure(in); got != 0.18 {
		t.Errorf("got %v want 0.18", got)
	}
	if got := ParsePressure(nil); got != 0 {
		t.Errorf("missing file: got %v want 0", got)
	}
}

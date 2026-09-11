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

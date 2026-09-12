package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
)

// demoEntries is one enabled demo module, enough to exercise the store.
func demoEntries(t *testing.T) []module.Entry {
	t.Helper()
	m := system.New(true)
	return []module.Entry{{Module: m, Enabled: true, Avail: m.Detect(context.Background(), detect.Env{Demo: true})}}
}

var _ = config.Config{}

func sized(m Model, w, h int) Model {
	r, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return r.(Model)
}

func TestAutofallToLite(t *testing.T) {
	m := sized(New(Options{Hostname: "example"}), 80, 24)
	if m.mode != Lite || m.hint == "" {
		t.Fatalf("mode=%v hint=%q; want Lite with hint", m.mode, m.hint)
	}
	out := m.Render(80, 24)
	if lines := strings.Split(out, "\n"); len(lines) != 24 {
		t.Errorf("rendered %d lines at 80x24", len(lines))
	}
	if !strings.Contains(out, "sitrep") || !strings.Contains(out, "example") {
		t.Error("header missing wordmark or hostname")
	}
}

func TestAutofallClimbsBack(t *testing.T) {
	m := sized(sized(New(Options{}), 80, 24), 120, 40)
	if m.mode != Full || m.hint != "" {
		t.Fatalf("mode=%v hint=%q; want Full again after growing", m.mode, m.hint)
	}
	if m = sized(sized(New(Options{Mode: Lite}), 80, 24), 120, 40); m.mode != Lite {
		t.Fatalf("explicit --lite must stay Lite, got %v", m.mode)
	}
}

func TestFullStaysFull(t *testing.T) {
	m := sized(New(Options{}), 100, 30)
	if m.mode != Full || m.hint != "" {
		t.Fatalf("mode=%v hint=%q", m.mode, m.hint)
	}
	if lines := strings.Split(m.Render(100, 30), "\n"); len(lines) != 30 {
		t.Errorf("rendered %d lines at 100x30", len(lines))
	}
}

func TestDenseEmptyConfig(t *testing.T) {
	m := sized(New(Options{Mode: Dense}), 130, 44)
	if out := m.Render(130, 44); !strings.Contains(out, "dense_modules") {
		t.Error("expected empty-config notice")
	}
}

func TestDenseRefusesSmall(t *testing.T) {
	m := sized(New(Options{Mode: Dense}), 100, 30)
	if !strings.Contains(m.Render(100, 30), "dense layout needs") {
		t.Error("expected refusal message")
	}
}

func TestKeys(t *testing.T) {
	m := sized(New(Options{}), 100, 30)
	r, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if !r.(Model).help {
		t.Error("? should open help")
	}
	r, _ = r.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if r.(Model).help {
		t.Error("esc should close help")
	}
	_, cmd := r.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Error("q should quit")
	}
}

func TestFreezeDropsDataAndRestarts(t *testing.T) {
	m := sized(New(Options{Entries: demoEntries(t)}), 100, 30)
	r, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = r.(Model)
	if !m.frozen || !strings.Contains(m.Render(100, 30), "[frozen]") {
		t.Fatal("f should freeze and say so in the header")
	}
	r, cmd := m.Update(module.DataMsg{ID: "system", Data: "x"})
	m = r.(Model)
	if cmd != nil || m.store["system"].data != nil {
		t.Error("frozen: data must be dropped and no tick scheduled")
	}
	r, cmd = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if m = r.(Model); m.frozen || cmd == nil {
		t.Error("second f must unfreeze and restart collection")
	}
}

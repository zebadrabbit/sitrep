package module

import (
	"context"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/detect"
)

type stub struct {
	id    string
	after []string
}

func (s stub) ID() string                                    { return s.id }
func (s stub) Title() string                                 { return s.id }
func (s stub) Flags() Flags                                  { return Flags{After: s.after} }
func (stub) Detect(context.Context, detect.Env) Availability { return Availability{} }
func (stub) Interval() time.Duration                         { return time.Second }
func (stub) Collect(context.Context) (Data, error)           { return nil, nil }
func (stub) Update(tea.Msg) tea.Cmd                          { return nil }
func (stub) Card(Data, int) string                           { return "" }
func (stub) View(Data, int, int) string                      { return "" }
func (stub) Keys() []key.Binding                             { return nil }
func (stub) Info() string                                    { return "" }

func TestOneShotOrder(t *testing.T) {
	in := []Entry{{Module: stub{id: "overview"}}, {Module: stub{id: "ports", after: []string{"docker"}}}, {Module: stub{id: "system"}}, {Module: stub{id: "docker"}}}
	got := OneShotOrder(in)
	ids := ""
	for _, e := range got {
		ids += e.Module.ID() + " "
	}
	if ids != "overview docker ports system " {
		t.Errorf("order = %q", ids)
	}
}

// Package overview composes every enabled module's Card into tab 1.
package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Data is assembled by the app at render time; Overview collects nothing.
type Data struct {
	Hostname, Distro, Kernel string
	Uptime                   time.Duration
	Load                     [3]float64
	OK, Failed, NeedsRoot    int
	Cards                    []Card
}

// Card is one module's contribution.
type Card struct {
	Title string
	Body  string
}

type Module struct{}

func New() *Module { return &Module{} }

func (*Module) ID() string                   { return "overview" }
func (*Module) Title() string                { return "Overview" }
func (*Module) Flags() module.Flags          { return module.Flags{} }
func (*Module) Interval() time.Duration      { return 0 }
func (*Module) Update(tea.Msg) tea.Cmd       { return nil }
func (*Module) Keys() []key.Binding          { return nil }
func (*Module) Card(module.Data, int) string { return "" }
func (*Module) Detect(context.Context, detect.Env) module.Availability {
	return module.Availability{State: module.Available, Reason: "composed from enabled modules"}
}
func (*Module) Collect(context.Context) (module.Data, error) { return nil, nil }

func (*Module) Info() string {
	return "Composes the Card of every enabled module. Collects nothing itself; the header " +
		"(hostname, distro, kernel, uptime, load) comes from the system module."
}

func (*Module) View(d module.Data, w, h int) string {
	od, _ := d.(Data)
	s := theme.Current()
	head := fmt.Sprintf("%s  %s  %s  up %s  load %.2f %.2f %.2f",
		s.Bold.Render(od.Hostname), od.Distro, od.Kernel, ui.Age(od.Uptime), od.Load[0], od.Load[1], od.Load[2])
	status := fmt.Sprintf("%s %d ok  %s %d failed  %s %d needs-root",
		s.OK.Render(s.Glyph.OK), od.OK, s.Crit.Render(s.Glyph.Fail), od.Failed, s.Warn.Render(s.Glyph.Degraded), od.NeedsRoot)
	blocks := make([]string, 0, len(od.Cards))
	cols := 1
	if w >= 100 {
		cols = 2
	}
	for _, c := range od.Cards {
		blocks = append(blocks, ui.Box(c.Title, c.Body, w/cols-1))
	}
	return strings.Join([]string{head, status, "", ui.Columns(blocks, cols, w)}, "\n")
}

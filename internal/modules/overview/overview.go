// Package overview composes every enabled module's Card into tab 1.
package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/insight"
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
	Insights                 []insight.Insight // top findings, crit first
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
		"(hostname, distro, kernel, uptime, load) comes from the system module. The lines under " +
		"the status row are the top findings from `sitrep why`, computed from the other modules' data."
}

// Columns is how many card columns fit in w. Cards need ~38 cells to show
// a bar and its numbers, so two columns from 76 up.
func Columns(w int) int {
	if w >= 76 {
		return 2
	}
	return 1
}

// CardWidth is the width a Card should render for in a w-wide overview.
func CardWidth(w int) int { return w/Columns(w) - 5 }

func (*Module) View(d module.Data, w, h int) string {
	od, _ := d.(Data)
	s := theme.Current()
	head := fmt.Sprintf("%s  %s  %s  up %s  load %.2f %.2f %.2f",
		s.Bold.Render(od.Hostname), od.Distro, od.Kernel, ui.Age(od.Uptime), od.Load[0], od.Load[1], od.Load[2])
	status := fmt.Sprintf("%s %d ok  %s %d failed  %s %d needs-root",
		s.OK.Render(s.Glyph.OK), od.OK, s.Crit.Render(s.Glyph.Fail), od.Failed, s.Warn.Render(s.Glyph.Degraded), od.NeedsRoot)
	blocks := make([]string, 0, len(od.Cards))
	cols := Columns(w)
	for _, c := range od.Cards {
		blocks = append(blocks, ui.Box(c.Title, c.Body, w/cols-1))
	}
	lines := []string{head, status}
	for _, i := range od.Insights[:min(maxInsights, len(od.Insights))] {
		lines = append(lines, Line(i, w))
	}
	return strings.Join(append(lines, "", ui.Columns(blocks, cols, w)), "\n")
}

// maxInsights is how many findings Overview shows; `sitrep why` has them all.
const maxInsights = 3

// Line renders one finding: glyph by level, text, then the tab to look at.
func Line(i insight.Insight, w int) string {
	s := theme.Current()
	glyph := s.Warn.Render(s.Glyph.Warn)
	if i.Level == insight.Crit {
		glyph = s.Crit.Render(s.Glyph.Crit)
	}
	line := fmt.Sprintf("%-2s %s  %s", glyph, i.Text, s.Dim.Render(s.Glyph.Sep+" "+i.Tab))
	if w > 0 {
		line = lipgloss.NewStyle().MaxWidth(w).Render(line)
	}
	return line
}

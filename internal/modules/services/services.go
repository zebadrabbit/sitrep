// Package services lists systemd units: failed pinned to the top, then
// active, then inactive collapsed (HANDOFF §5 #4).
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/collect"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Unit is one row of `systemctl list-units --output=json`.
type Unit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

// Data is one collection.
type Data struct {
	Units    []Unit `json:"units"`
	Active   int    `json:"active"`
	Failed   int    `json:"failed"`
	Inactive int    `json:"inactive"`
}

type Module struct {
	run  *collect.Runner
	ui   uiState
	demo bool
}

type uiState struct {
	sel     int
	filter  string
	typing  bool
	showAll bool
	visible int
}

func New(demo bool) *Module { return &Module{run: collect.New("services", demo), demo: demo} }

func (*Module) ID() string              { return "services" }
func (*Module) Title() string           { return "Services" }
func (*Module) Flags() module.Flags     { return module.Flags{} }
func (*Module) Interval() time.Duration { return 5 * time.Second }

var keys = []key.Binding{
	key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "inactive")),
}

func (*Module) Keys() []key.Binding { return keys }

func (*Module) Info() string {
	return `Collects: every systemd service unit with load/active/sub state. Failed
units are pinned to the top, then active, then inactive (collapsed; a shows
them). / filters by name or description.
Needs:    systemd. Other init systems show the module as unsupported.
Execs:    systemctl list-units --type=service --all --output=json --no-pager
Interval: 5s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if env.Init != "systemd" {
		return module.Availability{State: module.Unsupported, Reason: "unsupported init: " + env.Init}
	}
	if !detect.Has("systemctl") {
		return module.Availability{State: module.Missing, Reason: "systemctl missing"}
	}
	return module.Availability{State: module.Available, Reason: "systemd"}
}

// ParseUnits decodes the JSON array; a broken payload is an error, since
// there is no line-by-line recovery in a JSON document.
func ParseUnits(out []byte) ([]Unit, error) {
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var us []Unit
	if err := json.Unmarshal(out, &us); err != nil {
		return nil, fmt.Errorf("services: parse systemctl json: %w", err)
	}
	return us, nil
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, "systemctl", "list-units", "--type=service", "--all", "--output=json", "--no-pager")
	if err != nil {
		return nil, err
	}
	units, err := ParseUnits(res.Stdout)
	if err != nil {
		return nil, err
	}
	d := Data{Units: units}
	for _, u := range units {
		switch u.Active {
		case "failed":
			d.Failed++
		case "active":
			d.Active++
		default:
			d.Inactive++
		}
	}
	sort.SliceStable(d.Units, func(i, j int) bool {
		a, b := rank(d.Units[i]), rank(d.Units[j])
		if a != b {
			return a < b
		}
		return d.Units[i].Unit < d.Units[j].Unit
	})
	return d, nil
}

func rank(u Unit) int {
	switch u.Active {
	case "failed":
		return 0
	case "active":
		return 1
	}
	return 2
}

func (m *Module) Update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	u := &m.ui
	if u.typing {
		switch k.String() {
		case "enter", "esc":
			u.typing = false
		case "backspace":
			if len(u.filter) > 0 {
				u.filter = u.filter[:len(u.filter)-1]
			}
		default:
			if k.Text != "" {
				u.filter += k.Text
			}
		}
		u.sel = 0
		return nil
	}
	switch k.String() {
	case "j", "down":
		u.sel++
	case "k", "up":
		u.sel--
	case "/":
		u.typing, u.filter = true, ""
	case "esc":
		u.filter = ""
	case "a":
		u.showAll = !u.showAll
	}
	u.sel = max(0, min(u.sel, u.visible-1))
	return nil
}

func (*Module) Card(d module.Data, w int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	line := fmt.Sprintf("%s active", s.Bold.Render(fmt.Sprint(sd.Active)))
	if sd.Failed > 0 {
		line += "  " + s.Crit.Render(fmt.Sprintf("%s %d failed", s.Glyph.Fail, sd.Failed))
	} else {
		line += "  " + s.OK.Render(s.Glyph.OK+" none failed")
	}
	lines := []string{line}
	for _, u := range sd.Units {
		if u.Active == "failed" && len(lines) < 4 {
			lines = append(lines, s.Crit.Render(s.Glyph.Fail)+" "+strings.TrimSuffix(u.Unit, ".service")+"  "+s.Dim.Render(u.Description))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Module) View(d module.Data, w, h int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	f := strings.ToLower(m.ui.filter)
	rows := [][]string{}
	hidden := 0
	for _, u := range sd.Units {
		if f != "" && !strings.Contains(strings.ToLower(u.Unit+" "+u.Description), f) {
			continue
		}
		if u.Active != "failed" && u.Active != "active" && !m.ui.showAll && f == "" {
			hidden++
			continue
		}
		g := s.Dim.Render(s.Glyph.Fail)
		switch u.Active {
		case "failed":
			g = s.Crit.Render(s.Glyph.Fail)
		case "active":
			g = s.OK.Render(s.Glyph.OK)
			if u.Sub == "exited" {
				g = s.Dim.Render(s.Glyph.OK)
			}
		}
		rows = append(rows, []string{g + " " + clip(strings.TrimSuffix(u.Unit, ".service"), 36), u.Active + "/" + u.Sub, s.Dim.Render(u.Description)})
	}
	m.ui.visible = len(rows)
	if m.ui.sel >= len(rows) {
		m.ui.sel = max(0, len(rows)-1)
	}
	lo, hi := 0, len(rows)
	if h > 0 {
		avail := max(1, h-3)
		if m.ui.sel >= avail {
			lo = m.ui.sel - avail + 1
		}
		hi = min(len(rows), lo+avail)
	}
	table := ui.Table([]string{"  UNIT", "STATE", "DESCRIPTION"}, rows[lo:hi], w-1)
	lines := strings.Split(table, "\n")
	lines[0] = " " + lines[0]
	for i := range lines[1:] {
		if h > 0 && lo+i == m.ui.sel {
			lines[i+1] = ui.Cursor(lines[i+1], w)
		} else {
			lines[i+1] = " " + lines[i+1]
		}
	}
	status := fmt.Sprintf("%d failed · %d active · %d inactive", sd.Failed, sd.Active, sd.Inactive)
	if hidden > 0 {
		status += fmt.Sprintf(" (%d hidden, a to show)", hidden)
	}
	if m.ui.typing || m.ui.filter != "" {
		status += " · filter: " + m.ui.filter
		if m.ui.typing {
			status += "▏"
		}
	}
	return strings.Join(append(lines, s.Dim.Render(status)), "\n")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

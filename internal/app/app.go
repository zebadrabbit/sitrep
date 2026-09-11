// Package app is the Bubble Tea root: layout modes, sidebar, footer, key routing.
package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// Mode is the layout (HANDOFF §7).
type Mode int

const (
	Full Mode = iota
	Lite
	Dense
)

// Minimum sizes per mode.
const (
	fullW, fullH   = 100, 30
	denseW, denseH = 130, 44
	sidebarW       = 16
)

// Options come from CLI flags.
type Options struct {
	Hostname string
	Mode     Mode
	Demo     bool
}

// Model is the root Bubble Tea model.
type Model struct {
	opts   Options
	w, h   int
	mode   Mode
	help   bool
	hint   string // one-time notice shown in the footer until the next key
	tabs   []string
	active int
}

// New builds the root model. Tabs are empty until Phase 1 registers modules.
func New(o Options) Model {
	return Model{opts: o, mode: o.Mode}
}

// Init requests nothing; the first frame paints from zero data.
func (m Model) Init() tea.Cmd { return nil }

// Update handles global keys and resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.autofall()
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.hint = ""
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = !m.help
	case "esc":
		m.help = false
	}
	return m, nil
}

// autofall drops full → lite when the terminal is too small, once, with a hint.
func (m *Model) autofall() {
	if m.mode == Full && (m.w < fullW || m.h < fullH) {
		m.mode = Lite
		m.hint = fmt.Sprintf("terminal %dx%d < %dx%d: using lite layout", m.w, m.h, fullW, fullH)
	}
}

// View renders the current frame in the alt screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.Render(m.w, m.h))
	v.AltScreen = true
	return v
}

// Render draws a frame at an explicit size; --once and screenshots use it too.
func (m Model) Render(w, h int) string {
	if w == 0 || h == 0 {
		return ""
	}
	if m.mode == Dense && (w < denseW || h < denseH) {
		msg := fmt.Sprintf("dense layout needs %dx%d, terminal is %dx%d", denseW, denseH, w, h)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, theme.Current().Warn.Render(msg))
	}
	body := m.body(w, h-2)
	if m.help {
		body = m.helpOverlay(w, h-2)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.header(w), body, m.footer(w))
}

func (m Model) header(w int) string {
	s := theme.Current()
	left := s.Accent.Render("sitrep") + " " + s.Dim.Render(s.Glyph.Sep) + " " + m.opts.Hostname
	if m.mode == Lite {
		left += "  " + m.tabLine()
	}
	if m.opts.Demo {
		left += "  " + s.Dim.Render("[demo]")
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(left)
}

func (m Model) body(w, h int) string {
	if m.mode != Full {
		return m.content(w, h)
	}
	side := lipgloss.NewStyle().Width(sidebarW).Height(h).Render(m.sidebar())
	sep := strings.TrimRight(strings.Repeat("│\n", h), "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, side, theme.Current().Dim.Render(sep), m.content(w-sidebarW-1, h))
}

func (m Model) sidebar() string {
	if len(m.tabs) == 0 {
		return theme.Current().Dim.Render(" no modules")
	}
	var b strings.Builder
	for i, t := range m.tabs {
		key := string(rune('1' + i))
		if i == 9 {
			key = "0"
		}
		line := " " + ui.Hotkey(key, " "+t)
		if i == m.active {
			line = theme.Current().Accent.Render("▎") + line[1:]
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m Model) tabLine() string {
	parts := make([]string, len(m.tabs))
	for i, t := range m.tabs {
		parts[i] = "[" + ui.Hotkey(string(rune('1'+i)), "") + "]" + t
	}
	return strings.Join(parts, " ")
}

func (m Model) content(w, h int) string {
	msg := theme.Current().Dim.Render("no modules registered yet")
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
}

func (m Model) footer(w int) string {
	s := theme.Current()
	keys := ui.Hotkey("q", "uit") + "  " + ui.Hotkey("?", " help")
	right := s.Warn.Render(m.hint)
	gap := w - lipgloss.Width(keys) - lipgloss.Width(right)
	if gap < 1 {
		right, gap = "", 1
	}
	return keys + strings.Repeat(" ", gap) + right
}

func (m Model) helpOverlay(w, h int) string {
	s := theme.Current()
	rows := []string{
		s.Bold.Render("keys"),
		ui.Hotkey("1-9,0", "  jump to tab"),
		ui.Hotkey("tab", "/") + ui.Hotkey("shift-tab", "  next / prev tab"),
		ui.Hotkey("j/k ↑/↓", "  move selection"),
		ui.Hotkey("enter", "  open detail   ") + ui.Hotkey("esc", "  back"),
		ui.Hotkey("/", "  filter   ") + ui.Hotkey("s", "  cycle sort   ") + ui.Hotkey("r", "  refresh"),
		ui.Hotkey("?", "  this help   ") + ui.Hotkey("q", "  quit"),
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.Accent.GetForeground()).Padding(0, 2).
		Render(strings.Join(rows, "\n"))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

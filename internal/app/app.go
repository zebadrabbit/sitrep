// Package app is the Bubble Tea root: layout modes, sidebar, tab routing,
// key handling, and the async collection loop (HANDOFF §4.2, §7).
package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules/overview"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
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

const (
	fullW, fullH   = 100, 30
	denseW, denseH = 130, 44
	sidebarW       = 16
)

var spinner = []string{"⠋", "⠙", "⠸", "⠴"}

// Options come from CLI flags.
type Options struct {
	Hostname string
	Mode     Mode
	Demo     bool
	Entries  []module.Entry
	Active   string   // module id to open first ("" = overview)
	Dense    []string // module ids for --view dense boxes (config dense_modules)
}

// Model is the root Bubble Tea model.
type Model struct {
	opts    Options
	w, h    int
	mode    Mode
	fell    bool // mode is Lite only because the terminal was too small
	help    bool
	hint    string // one-time notice in the footer until the next key
	entries []module.Entry
	tabs    []int // indexes into entries that are enabled, in hotkey order
	active  int   // index into tabs
	store   map[string]*slot
	frame   int
}

// New builds the root model from resolved entries.
func New(o Options) Model {
	m := Model{opts: o, mode: o.Mode, entries: o.Entries, store: map[string]*slot{}}
	for i, e := range o.Entries {
		if e.Enabled {
			if e.Module.ID() == o.Active {
				m.active = len(m.tabs)
			}
			m.tabs = append(m.tabs, i)
			m.store[e.Module.ID()] = &slot{}
		}
	}
	return m
}

// Once collects every module synchronously and renders one frame. Used by
// --once for screenshots and scripts; the TUI never blocks like this.
// keys are pressed after collection, so screenshots can show a detail pane.
func (m Model) Once(w, h int, keys []tea.KeyPressMsg) string {
	enabled := make([]module.Entry, 0, len(m.tabs))
	for _, ti := range m.tabs {
		enabled = append(enabled, m.entries[ti])
	}
	for _, e := range module.OneShotOrder(enabled) {
		if e.Module.Interval() == 0 {
			continue
		}
		msg := collectCmd(e.Module)().(module.DataMsg)
		r, _ := m.onData(msg)
		m = r.(Model)
	}
	if len(keys) > 0 {
		m.Render(w, h) // views record what is visible; keys index that list
	}
	for _, k := range keys {
		r, _ := m.Update(k)
		m = r.(Model)
	}
	return m.Render(w, h)
}

// Init starts every collector. The first frame paints before any returns.
func (m Model) Init() tea.Cmd { return m.startAll() }

// Update routes messages: size, data, ticks, global keys, then the active tab.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.autofall()
		return m, nil
	case module.DataMsg:
		m.frame++
		return m.onData(msg)
	case tickMsg:
		return m.onTick(msg)
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m.forward(msg)
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.hint = ""
	s := k.String()
	switch s {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "esc":
		if m.help {
			m.help = false
			return m, nil
		}
	case "tab":
		m.active = (m.active + 1) % max(1, len(m.tabs))
		return m, nil
	case "shift+tab":
		m.active = (m.active + len(m.tabs) - 1) % max(1, len(m.tabs))
		return m, nil
	case "r":
		return m, m.refresh()
	}
	// Digits, then per-module letters past the tenth tab (see module.Resolve).
	if len(s) == 1 {
		for i, ti := range m.tabs {
			if hk := m.entries[ti].Hotkey; hk != 0 && hk == rune(s[0]) {
				m.active = i
				return m, nil
			}
		}
	}
	return m.forward(k)
}

// forward hands the message to the active module's Update.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	if e := m.activeEntry(); e != nil {
		return m, e.Module.Update(msg)
	}
	return m, nil
}

// hotkey is the digit for an entry, or a space past the tenth tab (tab
// still cycles to it).
func hotkey(e module.Entry) string {
	if e.Hotkey == 0 {
		return " "
	}
	return string(e.Hotkey)
}

func (m Model) activeEntry() *module.Entry {
	if len(m.tabs) == 0 {
		return nil
	}
	return &m.entries[m.tabs[m.active]]
}

// autofall drops full → lite when the terminal is too small, with a hint,
// and climbs back once it is big enough again.
func (m *Model) autofall() {
	small := m.w < fullW || m.h < fullH
	switch {
	case m.mode == Full && small:
		m.mode, m.fell = Lite, true
		m.hint = fmt.Sprintf("terminal %dx%d < %dx%d: using lite layout", m.w, m.h, fullW, fullH)
	case m.fell && !small:
		m.mode, m.fell, m.hint = Full, false, ""
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
	if m.mode == Dense {
		if w < denseW || h < denseH {
			msg := fmt.Sprintf("dense layout needs %dx%d, terminal is %dx%d", denseW, denseH, w, h)
			return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, theme.Current().Warn.Render(msg))
		}
		return m.dense(w, h)
	}
	body := m.body(w, h-2)
	if m.help {
		body = m.helpOverlay(w, h-2)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.header(w), body, m.footer(w))
}

// contentWidth is the width the active tab's View receives.
func (m Model) contentWidth() int {
	if m.mode == Full {
		return m.w - sidebarW - 1
	}
	return m.w
}

func (m Model) collecting() bool {
	for _, s := range m.store {
		if s.collecting {
			return true
		}
	}
	return false
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
	if m.collecting() {
		left += " " + s.Dim.Render(spinner[m.frame%len(spinner)])
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
	s := theme.Current()
	if len(m.tabs) == 0 {
		return s.Dim.Render(" no modules")
	}
	var b strings.Builder
	for i, ti := range m.tabs {
		e := m.entries[ti]
		line := ui.Hotkey(hotkey(e), " "+e.Module.Title())
		if sl := m.store[e.Module.ID()]; sl != nil && sl.err != nil {
			line += " " + s.Warn.Render(s.Glyph.Warn)
		}
		if i == m.active {
			line = ui.Cursor(line, sidebarW)
		} else {
			line = " " + line
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m Model) tabLine() string {
	parts := make([]string, 0, len(m.tabs))
	for i, ti := range m.tabs {
		e := m.entries[ti]
		t := e.Module.Title()
		if i == m.active {
			t = theme.Current().Bold.Render(t)
		}
		parts = append(parts, "["+ui.Hotkey(hotkey(e), "")+"]"+t)
	}
	return strings.Join(parts, " ")
}

// content is the tab header line plus the module's View.
func (m Model) content(w, h int) string {
	e := m.activeEntry()
	if e == nil {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, theme.Current().Dim.Render("no modules enabled — see `sitrep modules list`"))
	}
	head := m.tabHeader(e, w)
	var view string
	if e.Module.ID() == "overview" {
		view = e.Module.View(m.overviewData(), w, h-2)
	} else {
		view = e.Module.View(m.store[e.Module.ID()].data, w, h-2)
	}
	box := lipgloss.NewStyle().Width(w).Height(h - 2).MaxHeight(h - 2).MaxWidth(w)
	return lipgloss.JoinVertical(lipgloss.Left, head, "", box.Render(view))
}

func (m Model) tabHeader(e *module.Entry, w int) string {
	s := theme.Current()
	left := s.Bold.Render(e.Module.Title())
	var right string
	if sl := m.store[e.Module.ID()]; sl != nil && e.Module.Interval() > 0 {
		switch {
		case sl.err != nil:
			right = s.Warn.Render(s.Glyph.Warn+" ") + s.Warn.Render(lipgloss.NewStyle().MaxWidth(w/2).Render(sl.err.Error()))
		case sl.updated.IsZero():
			right = s.Dim.Render("collecting…")
		default:
			right = s.Dim.Render(fmt.Sprintf("%s ago · %s", ui.Age(time.Since(sl.updated)), sl.took.Round(time.Millisecond)))
		}
	}
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// overviewData assembles tab 1 from the store at render time.
func (m Model) overviewData() overview.Data {
	d := overview.Data{Hostname: m.opts.Hostname}
	if sd, ok := m.store["system"]; ok {
		if sys, ok := sd.data.(system.Data); ok {
			d.Distro, d.Kernel, d.Uptime, d.Load = sys.Distro, sys.Kernel, sys.Uptime, sys.Load
		}
	}
	for _, ti := range m.tabs {
		e := m.entries[ti]
		if e.Module.Interval() == 0 {
			continue
		}
		sl := m.store[e.Module.ID()]
		switch {
		case sl.err != nil:
			d.Failed++
		case e.Avail.State == module.NeedsRoot || e.Avail.State == module.Degraded:
			d.NeedsRoot++
		default:
			d.OK++
		}
		body := e.Module.Card(sl.data, overview.CardWidth(m.contentWidth()))
		if sl.err != nil && sl.data == nil {
			body = theme.Current().Warn.Render(theme.Current().Glyph.Warn + " " + sl.err.Error())
		}
		d.Cards = append(d.Cards, overview.Card{Title: e.Module.Title(), Body: body})
	}
	return d
}

func (m Model) footer(w int) string {
	s := theme.Current()
	keys := ui.Hotkey("q", "uit") + "  " + ui.Hotkey("?", " help") + "  " + ui.Hotkey("r", "efresh")
	if e := m.activeEntry(); e != nil && len(e.Module.Keys()) > 0 {
		parts := []string{}
		for _, k := range e.Module.Keys() {
			parts = append(parts, ui.Hotkey(k.Help().Key, " "+k.Help().Desc))
		}
		keys += "  " + s.Dim.Render("│") + "  " + strings.Join(parts, "  ")
	}
	right := s.Warn.Render(m.hint)
	gap := w - lipgloss.Width(keys) - lipgloss.Width(right)
	if gap < 1 {
		right, gap = "", 1
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(keys + strings.Repeat(" ", gap) + right)
}

func (m Model) helpOverlay(w, h int) string {
	s := theme.Current()
	rows := []string{
		s.Bold.Render("keys"),
		ui.Hotkey("1-9,0", "  jump to tab   ") + ui.Hotkey("letter", "  tabs past ten (shown in sidebar)"),
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

// dense is the zero-chrome grid: 2×2 for up to four modules, 3×2 for up to
// six, each box showing the module's full View. Ports gets a full column
// when it is present (HANDOFF §6): it is listed first and spans both rows.
func (m Model) dense(w, h int) string {
	var ents []module.Entry
	for _, id := range m.opts.Dense {
		for _, ti := range m.tabs {
			if e := m.entries[ti]; e.Module.ID() == id && e.Module.Interval() > 0 {
				ents = append(ents, e)
			}
		}
	}
	if len(ents) == 0 {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, theme.Current().Dim.Render("dense_modules in config is empty"))
	}
	boxes := func(es []module.Entry, bw, bh int) []string {
		out := make([]string, 0, len(es))
		for _, e := range es {
			view := e.Module.View(m.store[e.Module.ID()].data, bw-2, bh-2)
			out = append(out, lipgloss.NewStyle().Height(bh).MaxHeight(bh).Render(ui.Box(e.Module.Title(), view, bw)))
		}
		return out
	}
	// Ports column on the left, everything else in a 2-row grid on the right.
	var left string
	rest := ents
	if ents[0].Module.ID() == "ports" {
		colW := w / 3
		left = boxes(ents[:1], colW, h)[0]
		rest = ents[1:]
		w -= colW
	}
	rows := 2
	cols := max(1, (len(rest)+rows-1)/rows)
	bw, bh := w/cols, h/rows
	grid := make([]string, 0, rows)
	for r := 0; r < rows && r*cols < len(rest); r++ {
		hi := min(len(rest), (r+1)*cols)
		grid = append(grid, lipgloss.JoinHorizontal(lipgloss.Top, boxes(rest[r*cols:hi], bw, bh)...))
	}
	right := lipgloss.JoinVertical(lipgloss.Left, grid...)
	if left == "" {
		return right
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

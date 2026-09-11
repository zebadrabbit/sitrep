package ports

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

type sortMode int

const (
	sortPort sortMode = iota
	sortAge
	sortConn
	sortProcess
)

func (s sortMode) String() string { return [...]string{"port", "age", "conn", "process"}[s] }

// uiState is tab-local: selection, sort, filter, detail, probe.
type uiState struct {
	sel       int
	sort      sortMode
	filter    string
	typing    bool
	hideLoop  bool
	detail    bool
	detailKey string
	probe     string
	probing   bool
	flat      bool            // c: no grouping
	expanded  map[string]bool // space: groups opened in place
	visible   []disp          // lines as last rendered, so keys index the same list
}

type probeMsg struct {
	key    string
	result string
}

var keys = []key.Binding{
	key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail")),
	key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
	key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "loopback")),
	key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "expand")),
	key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "flat")),
	key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "probe")),
}

func (*Module) Keys() []key.Binding { return keys }

// Update handles tab-local keys. Selection is clamped at render time.
func (m *Module) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case probeMsg:
		m.ui.probing = false
		if msg.key == m.ui.detailKey {
			m.ui.probe = msg.result
		}
		return nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return nil
}

func (m *Module) key(k tea.KeyPressMsg) tea.Cmd {
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
	case "g", "home":
		u.sel = 0
	case "G", "end":
		u.sel = len(u.visible) - 1
	case "enter":
		if u.sel < len(u.visible) {
			u.detail, u.detailKey, u.probe = true, rowKey(u.visible[u.sel].Row), ""
		}
	case "space":
		if u.sel < len(u.visible) {
			if d := u.visible[u.sel]; d.Key != "" && !d.Member {
				if u.expanded == nil {
					u.expanded = map[string]bool{}
				}
				u.expanded[d.Key] = !u.expanded[d.Key]
			}
		}
	case "c":
		u.flat = !u.flat
		u.sel = 0
	case "esc":
		u.detail, u.filter = false, ""
	case "s":
		u.sort = (u.sort + 1) % 4
	case "/":
		u.typing, u.filter = true, ""
	case "L":
		u.hideLoop = !u.hideLoop
	case "p":
		if u.detail && !u.probing && u.sel < len(u.visible) {
			u.probing = true
			r := u.visible[u.sel].Row
			return probeCmd(rowKey(r), r.Port, r.Identity.HTTP)
		}
	}
	if u.sel < 0 {
		u.sel = 0
	}
	if n := len(u.visible); n > 0 && u.sel >= n {
		u.sel = n - 1
	}
	return nil
}

func (*Module) Card(d module.Data, w int) string {
	pd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	lines := []string{
		fmt.Sprintf("%s listening   %s established", s.Bold.Render(strconv.Itoa(pd.Listening)), s.Bold.Render(strconv.Itoa(pd.Established))),
	}
	if pd.Newest != "" {
		lines = append(lines, "newest  "+pd.Newest)
	}
	if pd.Unknown > 0 {
		lines = append(lines, s.Dim.Render(fmt.Sprintf("%d unidentified", pd.Unknown)))
	}
	return strings.Join(lines, "\n")
}

// View is the list, or the detail pane when open. h == 0 means unbounded.
func (m *Module) View(d module.Data, w, h int) string {
	pd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	rows := m.filtered(pd.Rows)
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j], m.ui.sort) })
	lines := flat(rows)
	if !m.ui.flat {
		lines = group(rows, m.ui.expanded)
	}
	m.ui.visible = lines
	if m.ui.sel >= len(lines) {
		m.ui.sel = max(0, len(lines)-1)
	}
	if m.ui.detail && len(lines) > 0 {
		return m.detailView(lines[m.ui.sel].Row, w, h)
	}
	return m.listView(lines, len(rows), w, h, pd.Fallback)
}

func (m *Module) filtered(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	f := strings.ToLower(m.ui.filter)
	for _, r := range rows {
		if m.ui.hideLoop && r.Loopback {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(rowText(r)), f) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func rowText(r Row) string {
	return strings.Join([]string{r.Proto, r.Addr, strconv.Itoa(r.Port), r.Process, r.User, r.Identity.Name}, " ")
}

func less(a, b Row, mode sortMode) bool {
	switch mode {
	case sortAge:
		if !a.Since.Equal(b.Since) {
			return a.Since.After(b.Since) // newest first
		}
	case sortConn:
		if a.Conn != b.Conn {
			return a.Conn > b.Conn
		}
	case sortProcess:
		if a.Process != b.Process {
			return a.Process < b.Process
		}
	}
	if a.Port != b.Port {
		return a.Port < b.Port
	}
	if a.Proto != b.Proto {
		return a.Proto < b.Proto
	}
	return a.Addr < b.Addr
}

func (m *Module) listView(rows []disp, total, w, h int, fallback bool) string {
	s := theme.Current()
	header := []string{"PROTO", "ADDR:PORT", "PROCESS", "USER", "AGE", "CONN", "IDENTITY"}
	cells := make([][]string, 0, len(rows))
	for _, d := range rows {
		cells = append(cells, cellsFor(d))
	}
	// Lite (80 cols) has no room for USER; the detail pane still shows it.
	if w < 90 {
		header = append(header[:3], header[4:]...)
		for i := range cells {
			cells[i] = append(cells[i][:3], cells[i][4:]...)
		}
	}
	// Window the rows around the selection so the table fits h.
	lo, hi := 0, len(rows)
	if h > 0 {
		avail := max(1, h-3)
		if m.ui.sel >= avail {
			lo = m.ui.sel - avail + 1
		}
		hi = min(len(rows), lo+avail)
	}
	table := ui.Table(header, cells[lo:hi], w-1)
	lines := strings.Split(table, "\n")
	lines[0] = " " + lines[0]
	for i := range lines[1:] {
		d := rows[lo+i]
		cursor := " "
		if h > 0 && lo+i == m.ui.sel {
			cursor = s.Accent.Render("▎")
		}
		switch {
		case d.Row.Gone:
			lines[i+1] = cursor + s.Dim.Strikethrough(true).Render(stripANSI(lines[i+1]))
		case d.Row.Loopback, d.Member:
			lines[i+1] = cursor + s.Dim.Render(stripANSI(lines[i+1]))
		default:
			lines[i+1] = cursor + lines[i+1]
		}
	}
	status := fmt.Sprintf("%d listeners", total)
	if !m.ui.flat {
		status += fmt.Sprintf(" in %d groups", len(rows))
	}
	status += " · sort " + m.ui.sort.String()
	if m.ui.hideLoop {
		status += " · loopback hidden"
	}
	if m.ui.typing || m.ui.filter != "" {
		status += " · filter: " + m.ui.filter
		if m.ui.typing {
			status += "▏"
		}
	}
	if fallback {
		status += " · " + s.Warn.Render(s.Glyph.Degraded+" ss missing, /proc/net fallback")
	}
	return strings.Join(append(lines, s.Dim.Render(status)), "\n")
}

// cellsFor renders one line's columns with glyphs. Group leaders show the
// fold count after the address; expanded members hang under them.
func cellsFor(d disp) []string {
	r := d.Row
	s := theme.Current()
	g := s.Glyph
	addr := addrPort(r, 24)
	switch {
	case d.Member:
		addr = "└ " + addrPort(r, 22)
	case len(d.Members) > 0:
		mark := "+"
		if d.Expanded {
			mark = "−"
		}
		addr = addrPort(r, 19) + " " + s.Dim.Render(fmt.Sprintf("%s%d", mark, len(d.Members)))
	}
	proc, user, age := s.Warn.Render(g.Degraded), s.Warn.Render(g.Degraded), s.Warn.Render(g.Degraded)
	if r.Process != "" {
		proc = clip(r.Process, 12) + " (" + strconv.Itoa(r.PID) + ")"
		if r.Container != "" {
			proc = "docker" + g.Sep + clip(r.Container, 17)
		} else if r.Identity.Source == SrcDocker {
			proc = "docker" + g.Sep + "?"
		}
	}
	if r.User != "" {
		user = clip(r.User, 10)
	}
	if !r.Since.IsZero() {
		age = ageStr(r.Age)
	}
	conn := strconv.Itoa(r.Conn)
	if strings.HasPrefix(r.Proto, "udp") {
		conn = "—"
	}
	mark := " "
	if r.New {
		mark = s.OK.Render(g.New)
	}
	conf := ui.Check(Glyph(r.Identity.Source, struct{ OK, Degraded, Fail string }{g.OK, g.Degraded, g.Fail}))
	if d.Member {
		// Members only repeat what differs from the leader: the pid.
		proc, user, age, conn = "", "", "", strconv.Itoa(r.Conn)
		if r.PID != d.LeaderPID && r.PID > 0 {
			proc = "(" + strconv.Itoa(r.PID) + ")"
		}
		if strings.HasPrefix(r.Proto, "udp") {
			conn = ""
		}
		return []string{" " + r.Proto, addr, proc, user, age, conn, ""}
	}
	return []string{mark + r.Proto, addr, proc, user, age, conn, conf + " " + r.Identity.Name}
}

// clip truncates to n cells with an ellipsis; column widths stay sane on
// long IPv6 addresses and 15-char comm names.
func clip(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// addrPort renders addr:port within n cells, clipping the address (never
// the port) and painting the port with theme.Port.
func addrPort(r Row, n int) string {
	addr := r.Addr
	if strings.Contains(addr, ":") {
		addr = "[" + addr + "]"
	}
	port := strconv.Itoa(r.Port)
	if n > 0 {
		addr = clip(addr, max(3, n-len(port)-1))
	}
	return addr + ":" + theme.Current().Port.Render(port)
}

func ageStr(d time.Duration) string { return ui.Age(d) }

func (m *Module) detailView(r Row, w, h int) string {
	s := theme.Current()
	d := r.Detail
	na := s.Warn.Render(s.Glyph.Degraded + " needs root")
	val := func(v string) string {
		if v == "" {
			return na
		}
		return v
	}
	fds := "?"
	if d.FDs >= 0 {
		fds = strconv.Itoa(d.FDs)
	}
	lines := []string{
		s.Bold.Render(addrPort(r, 0)) + "  " + r.Proto + "  " + ui.Check(Glyph(r.Identity.Source, struct{ OK, Degraded, Fail string }{s.Glyph.OK, s.Glyph.Degraded, s.Glyph.Fail})) + " " + r.Identity.Name,
		"",
		kv("process", val(r.Process)) + "   " + kv("pid/ppid", fmt.Sprintf("%d/%d", r.PID, r.PPID)) + "   " + kv("user", val(r.User)),
		kv("cmdline", val(d.Cmdline)),
		kv("exe", val(d.Exe)) + "   " + kv("cwd", val(d.Cwd)),
		kv("unit", val(d.Unit)) + "   " + kv("container", containerStr(r)),
		kv("cpu", fmt.Sprintf("%.1f%%", d.CPU)) + "   " + kv("rss", ui.Bytes(d.RSS)) + "   " + kv("threads", strconv.Itoa(d.Threads)) + "   " + kv("fds", fds),
		kv("since", sinceStr(r)),
		kv("running", sourceStr(r)),
		"",
		s.Bold.Render("identity") + "  " + s.Dim.Render("tried in order, first hit wins"),
		identityTrail(r),
		"",
		s.Bold.Render(fmt.Sprintf("peers (%d established)", r.Conn)),
	}
	if len(r.Peers) == 0 {
		lines = append(lines, s.Dim.Render("  none"))
	}
	for _, p := range r.Peers {
		lines = append(lines, fmt.Sprintf("  %-40s %s", p.Remote, s.Dim.Render(p.State)))
	}
	lines = append(lines, "", s.Bold.Render("probe")+"  "+s.Dim.Render("p — connects to 127.0.0.1:"+strconv.Itoa(r.Port)+", on demand only"))
	switch {
	case m.ui.probing:
		lines = append(lines, "  probing…")
	case m.ui.probe != "":
		lines = append(lines, "  "+strings.ReplaceAll(m.ui.probe, "\n", "\n  "))
	}
	out := strings.Join(lines, "\n")
	if h > 0 {
		out = lipgloss.NewStyle().MaxHeight(h).Render(out)
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(out)
}

func kv(k, v string) string { return theme.Current().Dim.Render(k+" ") + v }

func containerStr(r Row) string {
	if r.Container == "" {
		return theme.Current().Dim.Render("none")
	}
	return r.Container + "  " + theme.Current().Dim.Render(r.Image)
}

func sinceStr(r Row) string {
	if r.Since.IsZero() {
		return theme.Current().Warn.Render(theme.Current().Glyph.Degraded + " needs root")
	}
	return r.Since.Format("2006-01-02 15:04:05") + " (" + ageStr(r.Age) + " ago)"
}

// sourceStr: "/usr/sbin/sshd  modified 12d 4h ago" or a stale warning when
// the file changed after the process started.
func sourceStr(r Row) string {
	s := theme.Current()
	d := r.Detail
	if d.Source == "" {
		return s.Warn.Render(s.Glyph.Degraded + " needs root")
	}
	out := d.Source
	if !d.SourceMod.IsZero() {
		out += "  " + s.Dim.Render("modified "+ageStr(r.Since.Add(r.Age).Sub(d.SourceMod))+" ago")
	}
	if d.Stale {
		out += "  " + s.Warn.Render(s.Glyph.Warn+" changed since process started — running old code")
	}
	return out
}

func identityTrail(r Row) string {
	s := theme.Current()
	order := []string{SrcDocker, SrcProcess, SrcPortTable, SrcServices, SrcHeuristic}
	parts := make([]string, 0, len(order))
	for _, src := range order {
		if src == r.Identity.Source {
			parts = append(parts, s.OK.Render(s.Glyph.OK+" "+src))
			return "  " + strings.Join(parts, "  ")
		}
		parts = append(parts, s.Dim.Render(s.Glyph.Fail+" "+src))
	}
	return "  " + strings.Join(parts, "  ")
}

// stripANSI drops escape sequences so a whole line can be restyled.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

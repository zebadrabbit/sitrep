// Package sessions lists who is logged in, from where, and how idle
// (HANDOFF §5 #10).
package sessions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// Session is one login.
type Session struct {
	ID     string    `json:"id,omitempty"` // loginctl session id
	User   string    `json:"user"`
	TTY    string    `json:"tty"`
	Remote string    `json:"remote"`
	Since  time.Time `json:"since,omitempty"`
	State  string    `json:"state,omitempty"`
	Idle   bool      `json:"idle"`
}

// Data is one collection.
type Data struct {
	Sessions []Session `json:"sessions"`
	Users    int       `json:"users"` // distinct users
}

type Module struct {
	run *collect.Runner
}

func New(demo bool) *Module { return &Module{run: collect.New("sessions", demo)} }

func (*Module) ID() string              { return "sessions" }
func (*Module) Title() string           { return "Sessions" }
func (*Module) Flags() module.Flags     { return module.Flags{} }
func (*Module) Interval() time.Duration { return 5 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: logged-in sessions from who (user, tty, login time, remote host)
merged with loginctl (session id, state, idle) by tty. Your own ssh session
and any tunnel will be here; that is the point.
Needs:    who (coreutils). loginctl is optional and adds state/idle.
Execs:    who, loginctl list-sessions --output=json --no-pager
Interval: 5s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("who") {
		return module.Availability{State: module.Missing, Reason: "who missing (coreutils)"}
	}
	if !detect.Has("loginctl") {
		return module.Availability{State: module.Degraded, Reason: "who only; loginctl missing"}
	}
	return module.Availability{State: module.Available, Reason: "who + loginctl"}
}

// ParseWho: "winter   pts/0        2026-09-10 21:52 (10.0.0.9)".
func ParseWho(out []byte, now time.Time) []Session {
	var ss []Session
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		s := Session{User: f[0], TTY: f[1]}
		if t, err := time.ParseInLocation("2006-01-02 15:04", f[2]+" "+f[3], now.Location()); err == nil {
			s.Since = t
		}
		if len(f) > 4 {
			s.Remote = strings.Trim(f[4], "()")
		}
		ss = append(ss, s)
	}
	return ss
}

type loginctlRow struct {
	Session string `json:"session"`
	User    string `json:"user"`
	TTY     string `json:"tty"`
	State   string `json:"state"`
	Idle    bool   `json:"idle"`
}

// ParseLoginctl decodes the JSON list.
func ParseLoginctl(out []byte) []loginctlRow {
	var rows []loginctlRow
	_ = json.Unmarshal(out, &rows)
	return rows
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, "who")
	if err != nil {
		return nil, err
	}
	d := Data{Sessions: ParseWho(res.Stdout, time.Now())}
	if lc, err := m.run.Run(ctx, "loginctl", "list-sessions", "--output=json", "--no-pager"); err == nil {
		merge(d.Sessions, ParseLoginctl(lc.Stdout))
	}
	users := map[string]bool{}
	for _, s := range d.Sessions {
		users[s.User] = true
	}
	d.Users = len(users)
	return d, nil
}

// merge attaches loginctl state to who rows by tty; rows loginctl has but
// who doesn't (no tty, e.g. an sftp session) are ignored.
func merge(ss []Session, rows []loginctlRow) {
	for i := range ss {
		for _, r := range rows {
			if r.TTY == ss[i].TTY && r.User == ss[i].User {
				ss[i].ID, ss[i].State, ss[i].Idle = r.Session, r.State, r.Idle
			}
		}
	}
}

func (*Module) Card(d module.Data, w int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	lines := []string{fmt.Sprintf("%s users  %d sessions", s.Bold.Render(fmt.Sprint(sd.Users)), len(sd.Sessions))}
	for i, se := range sd.Sessions {
		if i == 3 {
			break
		}
		from := se.Remote
		if from == "" {
			from = "local"
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s  %s", s.OK.Render(s.Glyph.OK), se.User, se.TTY, s.Dim.Render(from)))
	}
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	rows := [][]string{}
	for _, se := range sd.Sessions {
		since := ""
		if !se.Since.IsZero() {
			since = ui.Age(time.Since(se.Since)) + " ago"
		}
		state := se.State
		if se.Idle {
			state += " idle"
		}
		from := se.Remote
		if from == "" {
			from = s.Dim.Render("local")
		}
		rows = append(rows, []string{s.OK.Render(s.Glyph.OK) + " " + se.User, se.TTY, from, since, s.Dim.Render(state)})
	}
	if len(rows) == 0 {
		return s.Dim.Render("nobody logged in")
	}
	out := ui.Table([]string{"  USER", "TTY", "FROM", "SINCE", "STATE"}, rows, w)
	if h > 0 {
		out = strings.Join(strings.Split(out, "\n")[:min(h, strings.Count(out, "\n")+1)], "\n")
	}
	return out
}

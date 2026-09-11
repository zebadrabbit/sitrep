// Package samba shows shares, connected sessions and open files
// (HANDOFF §5 #8). appliance_sensitive: off on appliances until acked.
package samba

import (
	"bufio"
	"bytes"
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

// Share is one [section] of testparm -s.
type Share struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Browseable bool   `json:"browseable"`
	Writable   bool   `json:"writable"`
	Guest      bool   `json:"guest"`
}

// Session is one smbstatus session.
type Session struct {
	User    string    `json:"user"`
	Machine string    `json:"machine"`
	Dialect string    `json:"dialect"`
	Shares  []string  `json:"shares"`
	Since   time.Time `json:"since,omitempty"`
}

// Data is one collection.
type Data struct {
	Shares    []Share   `json:"shares"`
	Sessions  []Session `json:"sessions"`
	OpenFiles int       `json:"open_files"`
	NeedsRoot bool      `json:"needs_root"` // smbstatus refused
	Version   string    `json:"version,omitempty"`
}

type Module struct {
	run  *collect.Runner
	demo bool
}

func New(demo bool) *Module { return &Module{run: collect.New("samba", demo), demo: demo} }

func (*Module) ID() string    { return "samba" }
func (*Module) Title() string { return "Samba" }
func (*Module) Flags() module.Flags {
	return module.Flags{ApplianceSensitive: true, NeedsRootForFull: true}
}
func (*Module) Interval() time.Duration { return 10 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: shares (path, browseable, writable, guest) from testparm, and
connected sessions (user, machine, dialect, shares) plus open file count from
smbstatus. smbstatus only works as root, so unprivileged shows shares and ◐.
Needs:    samba (testparm, smbstatus). appliance_sensitive: on TrueNAS,
Synology etc. it stays off until 'sitrep modules enable samba'.
Execs:    testparm -s, smbstatus -j
Interval: 10s.`
}

func (*Module) Detect(_ context.Context, env detect.Env) module.Availability {
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	if !detect.Has("testparm") {
		return module.Availability{State: module.Missing, Reason: "testparm missing (samba)"}
	}
	if !env.Root {
		return module.Availability{State: module.NeedsRoot, Reason: "smbstatus needs root; shares only"}
	}
	return module.Availability{State: module.Available, Reason: "testparm + smbstatus"}
}

// ParseTestparm reads the INI dump. [global] is skipped; defaults are
// browseable=yes, read only=yes, guest ok=no.
func ParseTestparm(out []byte) []Share {
	var shares []Share
	var cur *Share
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.Trim(line, "[]")
			if name == "global" {
				cur = nil
				continue
			}
			shares = append(shares, Share{Name: name, Browseable: true})
			cur = &shares[len(shares)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		k, v = strings.TrimSpace(k), strings.ToLower(strings.TrimSpace(v))
		yes := v == "yes" || v == "true"
		switch k {
		case "path":
			cur.Path = strings.TrimSpace(v)
		case "browseable", "browsable":
			cur.Browseable = yes
		case "read only":
			cur.Writable = !yes
		case "writable", "writeable", "write ok":
			cur.Writable = yes
		case "guest ok", "public":
			cur.Guest = yes
		}
	}
	return shares
}

type smbJSON struct {
	Version  string `json:"version"`
	Sessions map[string]struct {
		Username string `json:"username"`
		Remote   string `json:"remote_machine"`
		Dialect  string `json:"session_dialect"`
	} `json:"sessions"`
	Tcons map[string]struct {
		Service     string `json:"service"`
		SessionID   string `json:"session_id"`
		ConnectedAt string `json:"connected_at"`
	} `json:"tcons"`
	OpenFiles map[string]any `json:"open_files"`
}

// ParseSmbstatus joins sessions with their tree connects.
func ParseSmbstatus(out []byte) ([]Session, int, string, error) {
	var j smbJSON
	if err := json.Unmarshal(out, &j); err != nil {
		return nil, 0, "", fmt.Errorf("samba: parse smbstatus: %w", err)
	}
	byID := map[string]*Session{}
	var ids []string
	for id, s := range j.Sessions {
		byID[id] = &Session{User: s.Username, Machine: s.Remote, Dialect: s.Dialect}
		ids = append(ids, id)
	}
	for _, t := range j.Tcons {
		if s, ok := byID[t.SessionID]; ok {
			s.Shares = append(s.Shares, t.Service)
			if at, err := time.Parse(time.RFC3339Nano, t.ConnectedAt); err == nil && (s.Since.IsZero() || at.Before(s.Since)) {
				s.Since = at
			}
		}
	}
	sort.Strings(ids)
	out2 := make([]Session, 0, len(ids))
	for _, id := range ids {
		sort.Strings(byID[id].Shares)
		out2 = append(out2, *byID[id])
	}
	return out2, len(j.OpenFiles), j.Version, nil
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	res, err := m.run.Run(ctx, "testparm", "-s")
	if err != nil {
		return nil, err
	}
	d := Data{Shares: ParseTestparm(res.Stdout)}
	st, err := m.run.Run(ctx, "smbstatus", "-j")
	switch {
	case err != nil && strings.Contains(err.Error(), "only works as root"):
		d.NeedsRoot = true
	case err != nil:
		if !m.demo {
			return nil, err
		}
		d.NeedsRoot = true
	default:
		d.Sessions, d.OpenFiles, d.Version, err = ParseSmbstatus(st.Stdout)
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (*Module) Card(d module.Data, w int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	clients := s.Warn.Render(s.Glyph.Degraded + " needs root")
	if !sd.NeedsRoot {
		clients = fmt.Sprintf("%s connected", s.Bold.Render(fmt.Sprint(len(sd.Sessions))))
	}
	lines := []string{fmt.Sprintf("%s shares   %s", s.Bold.Render(fmt.Sprint(len(sd.Shares))), clients)}
	for i, se := range sd.Sessions {
		if i == 3 {
			break
		}
		lines = append(lines, fmt.Sprintf("%s %s@%s  %s", s.OK.Render(s.Glyph.OK), se.User, se.Machine, s.Dim.Render(strings.Join(se.Shares, " "))))
	}
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	sd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	yn := func(b bool) string {
		if b {
			return s.OK.Render(s.Glyph.OK)
		}
		return s.Dim.Render(s.Glyph.Fail)
	}
	rows := [][]string{}
	for _, sh := range sd.Shares {
		rows = append(rows, []string{sh.Name, s.Dim.Render(sh.Path), yn(sh.Browseable), yn(sh.Writable), yn(sh.Guest)})
	}
	out := []string{s.Bold.Render("shares"), ui.Table([]string{"NAME", "PATH", "BROWSE", "WRITE", "GUEST"}, rows, w), ""}
	switch {
	case sd.NeedsRoot:
		out = append(out, s.Bold.Render("sessions")+"  "+s.Warn.Render(s.Glyph.Degraded+" smbstatus only works as root"))
	default:
		out = append(out, s.Bold.Render(fmt.Sprintf("sessions (%d)", len(sd.Sessions)))+"  "+s.Dim.Render(fmt.Sprintf("%d open files · samba %s", sd.OpenFiles, sd.Version)))
		srows := [][]string{}
		for _, se := range sd.Sessions {
			since := ""
			if !se.Since.IsZero() {
				since = ui.Age(time.Since(se.Since)) + " ago"
			}
			srows = append(srows, []string{se.User, se.Machine, se.Dialect, strings.Join(se.Shares, " "), s.Dim.Render(since)})
		}
		if len(srows) == 0 {
			out = append(out, s.Dim.Render("  none"))
		} else {
			out = append(out, ui.Table([]string{"USER", "MACHINE", "DIALECT", "SHARES", "SINCE"}, srows, w))
		}
	}
	res := strings.Join(out, "\n")
	if h > 0 {
		res = strings.Join(strings.Split(res, "\n")[:min(h, strings.Count(res, "\n")+1)], "\n")
	}
	return res
}

// Package samba shows shares, connected sessions and open files
// (HANDOFF §5 #8). appliance_sensitive: off on appliances until acked.
package samba

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	ValidUsers string `json:"valid_users,omitempty"`
	ForceUser  string `json:"force_user,omitempty"`
	HostsAllow string `json:"hosts_allow,omitempty"`
	HostsDeny  string `json:"hosts_deny,omitempty"`
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
	// Global is the effective [global] config, allowlisted to what the server
	// block shows (globalKeys); the full 480-key dump is not worth a snapshot.
	Global map[string]string `json:"global"`
	// Units is smbd / nmbd / winbind → ActiveState.
	Units map[string]string `json:"units"`
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
Also the effective [global] config (server role, workgroup, netbios name,
protocol range, signing, encryption, auth backend, guest mapping, interfaces,
hosts allow/deny, log level) and the smbd / nmbd / winbind unit states; per
share the valid users, force user and hosts allow.
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

// globalKeys is what the server block shows; everything else in [global]
// is dropped. vfs objects / fruit are deliberately not here (owner's call:
// a module of their own if wanted, see docs/MODULES.md).
var globalKeys = []string{
	"server role", "workgroup", "netbios name", "server string",
	"server min protocol", "server max protocol", "server signing", "server smb encrypt",
	"server multi channel support", "smb ports",
	"security", "passdb backend", "ntlm auth", "map to guest", "guest account",
	"interfaces", "bind interfaces only", "hosts allow", "hosts deny",
	"usershare allow guests", "log level",
}

// ParseTestparm reads the INI dump (`testparm -sv`: every effective value).
// [global] keeps globalKeys; share defaults are browseable=yes, read
// only=yes, guest ok=no for the plain `-s` form that omits defaults.
func ParseTestparm(out []byte) (map[string]string, []Share) {
	global := map[string]string{}
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
		k, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, raw = strings.TrimSpace(k), strings.TrimSpace(raw)
		if cur == nil {
			if slices.Contains(globalKeys, k) {
				global[k] = raw
			}
			continue
		}
		v := strings.ToLower(raw)
		yes := v == "yes" || v == "true"
		switch k {
		case "path":
			cur.Path = raw
		case "valid users":
			cur.ValidUsers = raw
		case "force user":
			cur.ForceUser = raw
		case "hosts allow":
			cur.HostsAllow = raw
		case "hosts deny":
			cur.HostsDeny = raw
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
	return global, shares
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
	res, err := m.run.Run(ctx, "testparm", "-sv")
	if err != nil {
		return nil, err
	}
	var d Data
	d.Global, d.Shares = ParseTestparm(res.Stdout)
	d.Units = map[string]string{}
	if u, err := m.run.RunNamed(ctx, "systemctl_show_samba.txt", "systemctl", "show",
		"smbd.service", "nmbd.service", "winbind.service", "-p", "Id,ActiveState"); err == nil {
		for id, p := range collect.ShowProps(u.Stdout) {
			d.Units[id] = p["ActiveState"]
		}
	}
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
	if d.Version == "" {
		// smbstatus carries the version but needs root; smbd --version does not.
		if v, err := m.run.Run(ctx, "smbd", "--version"); err == nil {
			d.Version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(v.Stdout)), "Version "))
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
		users := sh.ValidUsers
		if sh.ForceUser != "" {
			users = strings.TrimSpace(users + " as " + sh.ForceUser)
		}
		rows = append(rows, []string{sh.Name, orAny(users), orAny(sh.HostsAllow), yn(sh.Browseable), yn(sh.Writable), yn(sh.Guest), s.Dim.Render(sh.Path)})
	}
	out := append(serverBlock(sd, w), s.Bold.Render("shares"), ui.Table([]string{"NAME", "USERS", "HOSTS", "BROWSE", "WRITE", "GUEST", "PATH"}, rows, w), "")
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

// serverBlock is four labeled groups of the effective [global] config plus
// the daemon states, each flowed to w, then a blank line. Empty when testparm
// gave no globals (an old fixture, or a smb.conf that is all shares).
func serverBlock(sd Data, w int) []string {
	if len(sd.Global) == 0 {
		return nil
	}
	s := theme.Current()
	g := func(k string) string { return sd.Global[k] }
	units := []string{}
	for _, u := range []string{"smbd", "nmbd", "winbind"} {
		glyph := s.Dim.Render(s.Glyph.Fail)
		if sd.Units[u] == "active" {
			glyph = s.OK.Render(s.Glyph.OK)
		}
		units = append(units, u+" "+glyph)
	}
	version := ""
	if sd.Version != "" {
		version = "samba " + sd.Version
	}
	interfaces := orAny(g("interfaces"))
	if g("bind interfaces only") == "Yes" {
		interfaces += " only"
	}
	var out []string
	row := func(label string, parts ...string) {
		out = append(out, flow(s.Dim.Render(fmt.Sprintf("%-7s", label)), parts, w)...)
	}
	row("server", strings.TrimSuffix(g("server role"), " server"), g("workgroup"), g("netbios name"), version, strings.Join(units, " "))
	row("proto", g("server min protocol")+" – "+g("server max protocol"), "signing "+g("server signing"), "encrypt "+g("server smb encrypt"), "multi-channel "+strings.ToLower(g("server multi channel support")), "ports "+g("smb ports"))
	row("auth", "security "+g("security"), "passdb "+g("passdb backend"), "ntlm "+g("ntlm auth"), "map to guest "+g("map to guest"), "guest "+g("guest account"))
	row("access", "interfaces "+interfaces, "hosts allow "+orAny(g("hosts allow")), "hosts deny "+orNone(g("hosts deny")), "usershare guests "+strings.ToLower(g("usershare allow guests")), "log level "+g("log level"))
	return append(out, "")
}

// flow joins parts with " · " onto lines no wider than w, the first behind
// label and the rest indented to match; a part never splits.
func flow(label string, parts []string, w int) []string {
	indent := strings.Repeat(" ", lipgloss.Width(label))
	lines := []string{label}
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		sep := " · "
		if first {
			sep = ""
		}
		if !first && lipgloss.Width(lines[len(lines)-1])+lipgloss.Width(sep+p) > w {
			lines = append(lines, indent)
			sep = ""
		}
		lines[len(lines)-1] += sep + p
		first = false
	}
	return lines
}

func orAny(v string) string {
	if v == "" {
		return "any"
	}
	return v
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

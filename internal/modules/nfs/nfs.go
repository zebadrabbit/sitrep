// Package nfs shows exports and connected clients (server side) and NFS
// client mounts (HANDOFF §5 #9). appliance_sensitive.
package nfs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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

// Export is one line of exportfs -v.
type Export struct {
	Path    string `json:"path"`
	Client  string `json:"client"`
	Options string `json:"options"`
}

// Mount is an NFS client mount from /proc/mounts.
type Mount struct {
	Source string `json:"source"`
	Target string `json:"target"`
	FSType string `json:"fstype"`
}

// Data is one collection.
type Data struct {
	Exports   []Export `json:"exports"`
	Clients   []string `json:"clients"` // from /proc/fs/nfsd/clients (root)
	Mounts    []Mount  `json:"mounts"`
	Server    bool     `json:"server"` // exportfs present
	NeedsRoot bool     `json:"needs_root"`
}

type Module struct {
	run  *collect.Runner
	demo bool
	root bool
}

func New(demo bool) *Module { return &Module{run: collect.New("nfs", demo), demo: demo} }

func (*Module) ID() string    { return "nfs" }
func (*Module) Title() string { return "NFS" }
func (*Module) Flags() module.Flags {
	return module.Flags{ApplianceSensitive: true, NeedsRootForFull: true}
}
func (*Module) Interval() time.Duration { return 10 * time.Second }
func (*Module) Update(tea.Msg) tea.Cmd  { return nil }
func (*Module) Keys() []key.Binding     { return nil }

func (*Module) Info() string {
	return `Collects: exports with client spec and options (exportfs -v), connected
clients from /proc/fs/nfsd/clients (root), and NFS mounts this box has as a
client from /proc/mounts.
Needs:    nfs-kernel-server for the server side; without it only client
mounts show. appliance_sensitive.
Execs:    exportfs -v. Reads /proc/mounts, /proc/fs/nfsd/clients/*/info.
Interval: 10s.`
}

func (m *Module) Detect(_ context.Context, env detect.Env) module.Availability {
	m.root = env.Root
	if env.Demo {
		return module.Availability{State: module.Available, Reason: "demo fixture"}
	}
	hasServer := detect.Has("exportfs")
	hasClient := hasNFSMounts()
	switch {
	case !hasServer && !hasClient:
		return module.Availability{State: module.Missing, Reason: "exportfs missing (nfs-kernel-server) and no nfs mounts"}
	case !hasServer:
		return module.Availability{State: module.Degraded, Reason: "client mounts only; exportfs missing"}
	case !env.Root:
		return module.Availability{State: module.NeedsRoot, Reason: "connected clients need root"}
	}
	return module.Availability{State: module.Available, Reason: "exportfs + /proc/fs/nfsd"}
}

func hasNFSMounts() bool {
	r := collect.New("nfs", false)
	b, err := r.ReadFile("/proc/mounts")
	return err == nil && len(ParseMounts(b)) > 0
}

// ParseExports handles the wrapped form where a long path sits alone on a
// line and the client(options) follows indented.
func ParseExports(out []byte) []Export {
	var ex []Export
	pending := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 1 && strings.HasPrefix(line, "/"):
			pending = f[0]
		case len(f) >= 2 && strings.HasPrefix(line, "/"):
			ex = append(ex, split(f[0], f[1]))
		case len(f) >= 1 && pending != "":
			ex = append(ex, split(pending, f[0]))
			pending = ""
		}
	}
	return ex
}

func split(path, spec string) Export {
	client, opts, _ := strings.Cut(spec, "(")
	return Export{Path: path, Client: client, Options: strings.TrimSuffix(opts, ")")}
}

// ParseMounts keeps nfs/nfs4 entries.
func ParseMounts(out []byte) []Mount {
	var ms []Mount
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && (f[2] == "nfs" || f[2] == "nfs4") {
			ms = append(ms, Mount{Source: f[0], Target: f[1], FSType: f[2]})
		}
	}
	return ms
}

func (m *Module) Collect(ctx context.Context) (module.Data, error) {
	var d Data
	res, err := m.run.Run(ctx, "exportfs", "-v")
	switch {
	case err == nil:
		d.Server = true
		d.Exports = ParseExports(res.Stdout)
	case errors.Is(err, collect.ErrMissing), errors.Is(err, collect.ErrNoFixture):
	default:
		return nil, err
	}
	if b, err := m.run.ReadFile("/proc/mounts"); err == nil {
		d.Mounts = ParseMounts(b)
	}
	if d.Server {
		m.clients(&d)
	}
	return d, nil
}

// clients reads /proc/fs/nfsd/clients/<id>/info, which is root-only.
func (m *Module) clients(d *Data) {
	dirs := m.run.Glob("/proc/fs/nfsd/clients/*")
	if len(dirs) == 0 && !m.root && !m.demo {
		d.NeedsRoot = true
		return
	}
	for _, dir := range dirs {
		b, err := m.run.ReadFile(dir + "/info")
		if err != nil {
			d.NeedsRoot = d.NeedsRoot || !m.root
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "address:"); ok {
				d.Clients = append(d.Clients, strings.Trim(strings.TrimSpace(v), "\""))
			}
		}
	}
}

func (*Module) Card(d module.Data, w int) string {
	nd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	lines := []string{}
	if nd.Server {
		clients := fmt.Sprintf("%s clients", s.Bold.Render(fmt.Sprint(len(nd.Clients))))
		if nd.NeedsRoot {
			clients = s.Warn.Render(s.Glyph.Degraded + " clients need root")
		}
		lines = append(lines, fmt.Sprintf("%s exports   %s", s.Bold.Render(fmt.Sprint(len(nd.Exports))), clients))
	} else {
		lines = append(lines, s.Dim.Render("not an NFS server"))
	}
	lines = append(lines, fmt.Sprintf("%s client mounts", s.Bold.Render(fmt.Sprint(len(nd.Mounts)))))
	return strings.Join(lines, "\n")
}

func (*Module) View(d module.Data, w, h int) string {
	nd, ok := d.(Data)
	if !ok {
		return theme.Current().Dim.Render("collecting…")
	}
	s := theme.Current()
	out := []string{}
	if nd.Server {
		rows := [][]string{}
		for _, e := range nd.Exports {
			rows = append(rows, []string{e.Path, e.Client, s.Dim.Render(e.Options)})
		}
		out = append(out, s.Bold.Render("exports"), ui.Table([]string{"PATH", "CLIENT", "OPTIONS"}, rows, w), "")
		switch {
		case nd.NeedsRoot:
			out = append(out, s.Bold.Render("connected clients")+"  "+s.Warn.Render(s.Glyph.Degraded+" needs root"))
		case len(nd.Clients) == 0:
			out = append(out, s.Bold.Render("connected clients")+"  "+s.Dim.Render("none"))
		default:
			out = append(out, s.Bold.Render("connected clients"), "  "+strings.Join(nd.Clients, "  "))
		}
		out = append(out, "")
	}
	rows := [][]string{}
	for _, mt := range nd.Mounts {
		rows = append(rows, []string{mt.Target, mt.Source, s.Dim.Render(mt.FSType)})
	}
	out = append(out, s.Bold.Render("client mounts"))
	if len(rows) == 0 {
		out = append(out, s.Dim.Render("  none"))
	} else {
		out = append(out, ui.Table([]string{"TARGET", "SOURCE", "TYPE"}, rows, w))
	}
	res := strings.Join(out, "\n")
	if h > 0 {
		res = strings.Join(strings.Split(res, "\n")[:min(h, strings.Count(res, "\n")+1)], "\n")
	}
	return res
}

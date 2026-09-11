package ports

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed ports.toml
var portsTOML []byte

// Layer 2: process name → identity (confidence ●).
var processTable = map[string]string{
	"sshd": "ssh", "smbd": "samba", "nmbd": "samba (netbios)", "ollama": "ollama api (http)",
	"tailscaled": "tailscale", "wg": "wireguard", "postgres": "postgres", "postmaster": "postgres",
	"redis-server": "redis", "valkey-server": "valkey", "nginx": "nginx (http)", "caddy": "caddy (http)",
	"apache2": "apache (http)", "httpd": "apache (http)", "haproxy": "haproxy", "traefik": "traefik (http)",
	"mosquitto": "mqtt", "pihole-FTL": "pi-hole dns", "dnsmasq": "dnsmasq", "unbound": "unbound dns",
	"named": "bind dns", "systemd-resolve": "systemd-resolved", "chronyd": "chrony ntp", "ntpd": "ntp",
	"cupsd": "cups (http)", "avahi-daemon": "avahi mdns", "rpcbind": "rpcbind", "rpc.mountd": "nfs mountd",
	"nfsd": "nfs", "containerd": "containerd", "dockerd": "docker daemon", "mysqld": "mysql", "mariadbd": "mariadb",
	"mongod": "mongodb", "memcached": "memcached", "prometheus": "prometheus (http)", "grafana": "grafana (http)",
	"node_exporter": "node-exporter (http)", "alertmanager": "alertmanager (http)", "influxd": "influxdb (http)",
	"jellyfin": "jellyfin (http)", "Plex Media Serv": "plex (http)", "go2rtc": "go2rtc", "frigate": "frigate (http)",
	"homeassistant": "home-assistant (http)", "hass": "home-assistant (http)", "syncthing": "syncthing (http)",
	"transmission-da": "transmission (http)", "qbittorrent-nox": "qbittorrent (http)", "miniserv.pl": "webmin (http)",
	"cockpit-ws": "cockpit (http)", "kong": "kong (http)", "minio": "minio (http)", "gitea": "gitea (http)",
	"forgejo": "forgejo (http)", "code-server": "code-server (http)", "vault": "vault (http)", "consul": "consul (http)",
	"exim4": "smtp", "postfix": "smtp", "master": "postfix", "dovecot": "imap/pop3", "vsftpd": "ftp", "proftpd": "ftp",
	"sslh": "sslh", "openvpn": "openvpn", "zerotier-one": "zerotier", "xrdp": "rdp", "vncserver": "vnc",
	"x11vnc": "vnc", "cloudflared": "cloudflare tunnel", "ngrok": "ngrok", "systemd": "systemd socket",
}

// Sources, in resolution order. Confidence glyph per source.
const (
	SrcDocker    = "docker"
	SrcProcess   = "process"
	SrcPortTable = "port-table"
	SrcServices  = "/etc/services"
	SrcHeuristic = "heuristic"
	SrcUnknown   = "unknown"
)

// Identity is a resolved name and how sure we are.
type Identity struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	HTTP   bool   `json:"http"` // probe should speak HTTP
}

type portTable struct {
	TCP map[string]string `toml:"tcp"`
	UDP map[string]string `toml:"udp"`
}

// resolver holds the tables; built once per module.
type resolver struct {
	ports    portTable
	services map[string]string // "tcp/22" → "ssh"
}

func newResolver(userDir string, services []byte) (*resolver, error) {
	r := &resolver{services: parseServices(services)}
	if _, err := toml.Decode(string(portsTOML), &r.ports); err != nil {
		return nil, fmt.Errorf("ports: embedded table: %w", err)
	}
	if userDir != "" {
		if b, err := os.ReadFile(filepath.Join(userDir, "ports.toml")); err == nil {
			var u portTable
			if _, err := toml.Decode(string(b), &u); err != nil {
				return nil, fmt.Errorf("ports: user ports.toml: %w", err)
			}
			for k, v := range u.TCP {
				r.ports.TCP[k] = v
			}
			for k, v := range u.UDP {
				r.ports.UDP[k] = v
			}
		}
	}
	return r, nil
}

// Resolve runs the layers in order; first hit wins.
func (r *resolver) Resolve(l Listener) Identity {
	base := strings.TrimSuffix(l.Proto, "6")
	if l.Container != "" {
		name := shortImage(l.Image)
		if isHexID(name) {
			name = l.Container // image tag was pruned; the container name says more than a digest
		}
		return Identity{Name: name, Source: SrcDocker, HTTP: r.httpish(base, l.Port)}
	}
	if l.Process == "docker-proxy" {
		// Docker module absent or not yet collected.
		return Identity{Name: "container", Source: SrcDocker, HTTP: true}
	}
	if n, ok := processTable[l.Process]; ok {
		return ident(n, SrcProcess)
	}
	tbl := r.ports.TCP
	if base == "udp" {
		tbl = r.ports.UDP
	}
	if n, ok := tbl[strconv.Itoa(l.Port)]; ok {
		return ident(n, SrcPortTable)
	}
	if n, ok := r.services[strconv.Itoa(l.Port)+"/"+base]; ok {
		return ident(n, SrcServices)
	}
	return heuristic(l)
}

// httpish consults the port table for the probe's sake when the identity
// came from somewhere else (a container image name says nothing about HTTP).
func (r *resolver) httpish(base string, port int) bool {
	tbl := r.ports.TCP
	if base == "udp" {
		tbl = r.ports.UDP
	}
	n, ok := tbl[strconv.Itoa(port)]
	return ok && strings.HasSuffix(n, "(http)") || port == 80 || port == 443 || port == 8080
}

// isHexID is true for untagged image references like 2d6f675dbf56.
func isHexID(s string) bool {
	if len(s) < 12 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// shortImage strips registry and tag: ghcr.io/plankanban/planka:2.1.1 → planka.
func shortImage(img string) string {
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	if i := strings.IndexAny(img, ":@"); i >= 0 {
		img = img[:i]
	}
	return img
}

func ident(n, src string) Identity {
	http := strings.HasSuffix(n, "(http)")
	return Identity{Name: strings.TrimSpace(strings.TrimSuffix(n, "(http)")), Source: src, HTTP: http}
}

var devRuntimes = map[string]bool{"node": true, "python3": true, "python": true, "bun": true, "deno": true,
	"ruby": true, "java": true, "php": true, "go": true, "flask": true, "gunicorn": true, "uvicorn": true, "npm": true}

// heuristic is the last resort: the process name, plus a cmdline hint when
// the process is a generic runtime ("? python3 -m homeassistant").
func heuristic(l Listener) Identity {
	if l.Process == "" {
		return Identity{Name: "?", Source: SrcUnknown}
	}
	name := "? " + l.Process
	if devRuntimes[l.Process] {
		if hint := cmdlineHint(l.Cmdline); hint != "" {
			name = "? " + hint
		}
		if l.Port >= 3000 && l.Port <= 9999 {
			return Identity{Name: name + " — likely dev server", Source: SrcHeuristic, HTTP: true}
		}
	}
	return Identity{Name: name, Source: SrcHeuristic}
}

// cmdlineHint keeps the interpreter and up to three args, path-stripped:
// "/usr/bin/python3 -P -m homeassistant --config /config" → "python3 -m homeassistant --config".
func cmdlineHint(cmdline string) string {
	f := strings.Fields(cmdline)
	if len(f) == 0 {
		return ""
	}
	out := []string{filepath.Base(f[0])}
	for _, a := range f[1:] {
		if a == "-P" || a == "-u" {
			continue // noise flags
		}
		out = append(out, filepath.Base(a))
		if len(out) == 4 {
			break
		}
	}
	return strings.Join(out, " ")
}

// parseServices reads /etc/services: "ssh 22/tcp # comment".
func parseServices(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if _, ok := out[f[1]]; !ok {
			out[f[1]] = f[0]
		}
	}
	return out
}

// Glyph returns the confidence marker for a source.
func Glyph(src string, g struct{ OK, Degraded, Fail string }) string {
	switch src {
	case SrcDocker, SrcProcess:
		return g.OK
	case SrcPortTable, SrcServices:
		return g.Degraded
	}
	return g.Fail
}

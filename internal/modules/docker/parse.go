package docker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// PortMap is one published port: host side → container side.
type PortMap struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Proto         string `json:"proto"`
}

// Container is one row of `docker ps -a`.
type Container struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Image   string    `json:"image"`
	State   string    `json:"state"`  // running, exited, paused, created
	Status  string    `json:"status"` // "Up 3 days (healthy)"
	Health  string    `json:"health"` // healthy, unhealthy, starting, ""
	Running bool      `json:"running"`
	Ports   []PortMap `json:"ports"`
	Project string    `json:"project,omitempty"` // compose project
	Created string    `json:"created"`
}

// psLine is the tolerant shape of one `{{json .}}` line. podman emits
// Names as an array; docker as a string.
type psLine struct {
	ID        string
	Names     json.RawMessage
	Image     string
	State     string
	Status    string
	Ports     string
	Labels    string
	CreatedAt string
}

// ParsePS parses newline-delimited JSON. Bad lines are skipped.
func ParsePS(out []byte) []Container {
	var cs []Container
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var l psLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		c := Container{ID: l.ID, Name: names(l.Names), Image: l.Image, State: strings.ToLower(l.State), Status: l.Status, Created: l.CreatedAt}
		c.Running = c.State == "running"
		c.Health = health(l.Status)
		c.Ports = ParsePorts(l.Ports)
		c.Project = label(l.Labels, "com.docker.compose.project")
		cs = append(cs, c)
	}
	return cs
}

func names(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimPrefix(strings.Split(s, ",")[0], "/")
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return strings.TrimPrefix(arr[0], "/")
	}
	return ""
}

func health(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	}
	return ""
}

func label(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// 0.0.0.0:3000->1337/tcp, [::]:3000->1337/tcp, 5432/tcp, 8444-8447/tcp, 0.0.0.0:8000-8001->8000-8001/tcp
var pubRe = regexp.MustCompile(`^(.*):(\d+)(?:-(\d+))?->(\d+)(?:-(\d+))?/(\w+)$`)

// ParsePorts keeps only published entries (those with ->), expanding ranges.
func ParsePorts(s string) []PortMap {
	var out []PortMap
	for _, e := range strings.Split(s, ",") {
		m := pubRe.FindStringSubmatch(strings.TrimSpace(e))
		if m == nil {
			continue
		}
		h0, _ := strconv.Atoi(m[2])
		h1 := h0
		if m[3] != "" {
			h1, _ = strconv.Atoi(m[3])
		}
		c0, _ := strconv.Atoi(m[4])
		for i := 0; h0+i <= h1; i++ {
			out = append(out, PortMap{HostIP: strings.Trim(m[1], "[]"), HostPort: h0 + i, ContainerPort: c0 + i, Proto: m[6]})
		}
	}
	return out
}

// ShortImage strips registry and tag: ghcr.io/plankanban/planka:2.1.1 → planka.
func ShortImage(img string) string {
	img = strings.TrimSuffix(img, "/")
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	if i := strings.IndexAny(img, ":@"); i >= 0 {
		img = img[:i]
	}
	return img
}

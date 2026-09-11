package ports

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// probeCmd is the only outbound network activity sitrep ever performs:
// localhost, on keypress, 1s. HTTP-ish identities get HEAD /; anything else
// gets a banner read (HANDOFF §6).
func probeCmd(k string, port int, httpish bool) tea.Cmd {
	return func() tea.Msg {
		return probeMsg{key: k, result: probe(port, httpish, time.Second)}
	}
}

func probe(port int, httpish bool, timeout time.Duration) string {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if httpish {
		return probeHTTP(addr, timeout)
	}
	return probeBanner(addr, timeout)
}

func probeHTTP(addr string, timeout time.Duration) string {
	c := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Head("http://" + addr + "/")
	if err != nil {
		// Many "http" ports are actually TLS; say so instead of guessing.
		if strings.Contains(err.Error(), "malformed HTTP") || strings.Contains(err.Error(), "server gave HTTP response") {
			return "no plain-HTTP response (TLS?)"
		}
		return "connect failed: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	out := resp.Status
	if s := resp.Header.Get("Server"); s != "" {
		out += "  Server: " + s
	}
	if l := resp.Header.Get("Location"); l != "" {
		out += "  Location: " + l
	}
	return out
}

func probeBanner(addr string, timeout time.Duration) string {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "connect failed: " + err.Error()
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 128)
	n, err := io.ReadAtLeast(bufio.NewReader(conn), buf, 1)
	if n == 0 {
		if err != nil && strings.Contains(err.Error(), "timeout") {
			return "connected; no banner within 1s"
		}
		return "connected; no banner"
	}
	return "banner: " + strings.TrimSpace(strings.ToValidUTF8(string(buf[:n]), "?"))
}

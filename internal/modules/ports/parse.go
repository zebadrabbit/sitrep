package ports

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// Listener is one row of `ss -tulnpH`.
type Listener struct {
	Proto   string // tcp, tcp6, udp, udp6
	Addr    string // local address without port
	Port    int
	PID     int    // 0 when ss could not see the owner (needs root)
	Process string // comm of the owning process, "" when unknown
	Cmdline string // filled from /proc by the collector; helps the heuristic
}

// Established is one row of `ss -tunaH` in a non-listening state.
type Established struct {
	Proto     string
	LocalPort int
	Remote    string
	State     string
}

// users:(("sshd",pid=13455,fd=3),("systemd",pid=1,fd=130))
var userRe = regexp.MustCompile(`\("([^"]*)",pid=(\d+),fd=\d+\)`)

// ParseListeners parses `ss -tulnpH`. Malformed lines are skipped, never fatal.
func ParseListeners(out []byte) []Listener {
	var ls []Listener
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if l, ok := parseListenerLine(sc.Text()); ok {
			ls = append(ls, l)
		}
	}
	return ls
}

func parseListenerLine(line string) (Listener, bool) {
	f := strings.Fields(line)
	// Netid State Recv-Q Send-Q Local Peer [Process]
	if len(f) < 6 {
		return Listener{}, false
	}
	addr, port, ok := splitAddr(f[4])
	if !ok {
		return Listener{}, false
	}
	l := Listener{Proto: protoOf(f[0], f[4]), Addr: addr, Port: port}
	if len(f) > 6 {
		l.Process, l.PID = owner(strings.Join(f[6:], " "))
	}
	return l, true
}

// ParseEstablished parses `ss -tunaH` keeping every non-LISTEN/UNCONN row.
func ParseEstablished(out []byte) []Established {
	var es []Established
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || f[1] == "LISTEN" || f[1] == "UNCONN" {
			continue
		}
		_, port, ok := splitAddr(f[4])
		if !ok {
			continue
		}
		es = append(es, Established{Proto: protoOf(f[0], f[4]), LocalPort: port, Remote: f[5], State: f[1]})
	}
	return es
}

// splitAddr handles 0.0.0.0:22, [::]:22, *:80, 127.0.0.1:8787, 10.0.0.1%eth0:68, [fe80::1]%eth0:546.
func splitAddr(s string) (addr string, port int, ok bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	addr = s[:i]
	if j := strings.Index(addr, "%"); j >= 0 {
		addr = addr[:j]
	}
	addr = strings.Trim(addr, "[]")
	return addr, port, true
}

// protoOf appends 6 for IPv6 locals. ss prints "*" for :: on some kernels.
func protoOf(netid, local string) string {
	if strings.HasSuffix(netid, "6") {
		return netid
	}
	if strings.HasPrefix(local, "[") || strings.HasPrefix(local, "*:") {
		return netid + "6"
	}
	return netid
}

// owner picks the lowest pid in the users list (the master, not a worker),
// except pid 1: a socket-activated service lists systemd too, and the
// service is the honest owner.
func owner(s string) (string, int) {
	best, name := 0, ""
	for _, m := range userRe.FindAllStringSubmatch(s, -1) {
		pid, _ := strconv.Atoi(m[2])
		switch {
		case best == 0, best == 1, pid < best && pid != 1:
			best, name = pid, m[1]
		}
	}
	return name, best
}

// IsLoopback is true for 127.x and ::1.
func IsLoopback(addr string) bool {
	return strings.HasPrefix(addr, "127.") || addr == "::1"
}

package ports

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Fallback for boxes without ss: /proc/net/{tcp,tcp6,udp,udp6}. Owners come
// from scanning /proc/*/fd for socket:[inode], which only works for our own
// processes unprivileged — the same limit ss has.

const (
	stateEstablished = "01"
	stateListen      = "0A"
)

// procNetRow is one parsed line of /proc/net/tcp*.
type procNetRow struct {
	Local, Remote string
	LocalPort     int
	State         string
	Inode         string
}

// ParseProcNet parses one /proc/net/* file. v6 selects address width.
func ParseProcNet(out []byte, v6 bool) []procNetRow {
	var rows []procNetRow
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		la, lp, err := hexAddr(f[1], v6)
		if err != nil {
			continue
		}
		ra, rp, _ := hexAddr(f[2], v6)
		rows = append(rows, procNetRow{Local: la, LocalPort: lp, Remote: fmt.Sprintf("%s:%d", ra, rp), State: f[3], Inode: f[9]})
	}
	return rows
}

// hexAddr decodes 0100007F:1F90 → 127.0.0.1, 8080.
func hexAddr(s string, v6 bool) (string, int, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", 0, fmt.Errorf("bad addr %q", s)
	}
	port, err := strconv.ParseInt(s[i+1:], 16, 32)
	if err != nil {
		return "", 0, err
	}
	b, err := hex.DecodeString(s[:i])
	if err != nil {
		return "", 0, err
	}
	if !v6 {
		if len(b) != 4 {
			return "", 0, fmt.Errorf("bad v4 %q", s)
		}
		return net.IPv4(b[3], b[2], b[1], b[0]).String(), int(port), nil
	}
	if len(b) != 16 {
		return "", 0, fmt.Errorf("bad v6 %q", s)
	}
	// Kernel writes each 32-bit word little-endian.
	ip := make(net.IP, 16)
	for w := 0; w < 4; w++ {
		ip[w*4], ip[w*4+1], ip[w*4+2], ip[w*4+3] = b[w*4+3], b[w*4+2], b[w*4+1], b[w*4]
	}
	return ip.String(), int(port), nil
}

// listenersFromProcNet converts rows to Listeners. udp rows with state 07
// (UNCONN) are listeners.
func listenersFromProcNet(rows []procNetRow, proto string, owners map[string]owned) []Listener {
	var ls []Listener
	udp := strings.HasPrefix(proto, "udp")
	for _, r := range rows {
		if (udp && r.State != "07") || (!udp && r.State != stateListen) {
			continue
		}
		l := Listener{Proto: proto, Addr: r.Local, Port: r.LocalPort}
		if o, ok := owners[r.Inode]; ok {
			l.PID, l.Process = o.pid, o.comm
		}
		ls = append(ls, l)
	}
	return ls
}

func establishedFromProcNet(rows []procNetRow, proto string) []Established {
	var es []Established
	for _, r := range rows {
		if r.State == stateEstablished {
			es = append(es, Established{Proto: proto, LocalPort: r.LocalPort, Remote: r.Remote, State: "ESTAB"})
		}
	}
	return es
}

type owned struct {
	pid  int
	comm string
}

// socketOwners maps socket inode → pid by reading every /proc/<pid>/fd we can.
func (m *Module) socketOwners() map[string]owned {
	out := map[string]owned{}
	for _, dir := range m.run.Glob("/proc/[0-9]*") {
		pid, err := strconv.Atoi(strings.TrimPrefix(dir, "/proc/"))
		if err != nil {
			continue
		}
		for _, fd := range m.run.Glob(dir + "/fd/*") {
			target, err := m.run.Readlink(fd)
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if _, seen := out[inode]; !seen {
				comm, _ := m.run.ReadFile(dir + "/comm")
				out[inode] = owned{pid: pid, comm: strings.TrimSpace(string(comm))}
			}
		}
	}
	return out
}

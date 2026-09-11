package ports

import (
	"os"
	"testing"
)

const ssListen = `tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=13455,fd=3),("systemd",pid=1,fd=130))
tcp LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=1164083,fd=13),("nginx",pid=1580,fd=13))
tcp LISTEN 0 4096 0.0.0.0:3000 0.0.0.0:* users:(("docker-proxy",pid=3623,fd=7))
tcp LISTEN 0 128 127.0.0.1:8787 0.0.0.0:* users:(("python3",pid=1466281,fd=8))
tcp LISTEN 0 4096 [::]:22 [::]:*
tcp LISTEN 0 4096 *:18555 *:*
udp UNCONN 0 0 0.0.0.0:51820 0.0.0.0:*
udp UNCONN 0 0 10.0.0.5%enp3s0f0:68 0.0.0.0:*
udp UNCONN 0 0 [fe80::1]%enp3s0f0:546 [::]:*
this line is garbage
tcp LISTEN 0 4096 127.0.0.1:notaport 0.0.0.0:*
`

func TestParseListeners(t *testing.T) {
	ls := ParseListeners([]byte(ssListen))
	if len(ls) != 9 {
		t.Fatalf("got %d listeners, want 9 (malformed lines skipped)", len(ls))
	}
	want := []struct {
		proto, addr string
		port, pid   int
		proc        string
	}{
		{"tcp", "0.0.0.0", 22, 13455, "sshd"}, // not pid 1: the socket-activated service owns it
		{"tcp", "0.0.0.0", 80, 1580, "nginx"},
		{"tcp", "0.0.0.0", 3000, 3623, "docker-proxy"},
		{"tcp", "127.0.0.1", 8787, 1466281, "python3"},
		{"tcp6", "::", 22, 0, ""},
		{"tcp6", "*", 18555, 0, ""},
		{"udp", "0.0.0.0", 51820, 0, ""},
		{"udp", "10.0.0.5", 68, 0, ""},
		{"udp6", "fe80::1", 546, 0, ""},
	}
	for i, w := range want {
		g := ls[i]
		if g.Proto != w.proto || g.Addr != w.addr || g.Port != w.port || g.PID != w.pid || g.Process != w.proc {
			t.Errorf("row %d: got %+v want %+v", i, g, w)
		}
	}
	if len(ParseListeners(nil)) != 0 {
		t.Error("empty input should give no rows")
	}
}

func TestParseEstablished(t *testing.T) {
	out := `tcp ESTAB 0 0 10.0.0.5:22 10.0.0.9:50103
tcp ESTAB 0 52 10.0.0.5:22 10.0.0.9:50104
tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*
tcp TIME-WAIT 0 0 10.0.0.5:445 10.0.0.9:1234
udp UNCONN 0 0 0.0.0.0:123 0.0.0.0:*
tcp ESTAB 0 0 [::ffff:10.0.0.5]:445 [::ffff:10.0.0.9]:5555
`
	es := ParseEstablished([]byte(out))
	if len(es) != 4 {
		t.Fatalf("got %d, want 4", len(es))
	}
	c := countEstablished(es)
	if c["tcp:22"] != 2 || c["tcp6:445"] != 1 || c["tcp:445"] != 0 {
		t.Errorf("counts = %v", c)
	}
}

func TestProcNetFallback(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0500000A:0016 0900000A:C3AB 01 00000000:00000000 00:00000000 00000000     0        0 99 1 0000000000000000 20 4 30 10 -1
   2: garbage
`
	rows := ParseProcNet([]byte(tcp), false)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	ls := listenersFromProcNet(rows, "tcp", map[string]owned{"12345": {pid: 42, comm: "python3"}})
	if len(ls) != 1 || ls[0].Addr != "127.0.0.1" || ls[0].Port != 8080 || ls[0].PID != 42 {
		t.Errorf("listeners = %+v", ls)
	}
	es := establishedFromProcNet(rows, "tcp")
	if len(es) != 1 || es[0].LocalPort != 22 || es[0].Remote != "10.0.0.9:50091" {
		t.Errorf("established = %+v", es)
	}
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 7 1 0000000000000000 100 0 0 10 0
`
	r6 := ParseProcNet([]byte(tcp6), true)
	if len(r6) != 1 || r6[0].Local != "::" || r6[0].LocalPort != 22 {
		t.Errorf("v6 = %+v", r6)
	}
}

func TestFixtureFileParses(t *testing.T) {
	b, err := os.ReadFile("../../../testdata/fixtures/ports/ss_tulnpH.txt")
	if err != nil {
		t.Skip("no captured fixture yet")
	}
	if ls := ParseListeners(b); len(ls) == 0 {
		t.Error("captured fixture parsed to zero rows")
	}
}

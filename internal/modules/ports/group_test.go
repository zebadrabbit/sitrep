package ports

import "testing"

func TestGroupFoldsAddressesAndFamilies(t *testing.T) {
	rows := []Row{
		{Proto: "udp", Addr: "172.17.0.1", Port: 137, PID: 1338, Process: "nmbd", Conn: 0},
		{Proto: "udp", Addr: "0.0.0.0", Port: 137, PID: 1338, Process: "nmbd"},
		{Proto: "udp", Addr: "10.0.0.5", Port: 137, PID: 1338, Process: "nmbd", New: true},
		{Proto: "tcp", Addr: "0.0.0.0", Port: 22, PID: 7, Process: "sshd", Conn: 2},
		{Proto: "tcp6", Addr: "::", Port: 22, PID: 7, Process: "sshd", Conn: 1},
		{Proto: "tcp", Addr: "127.0.0.1", Port: 5432, PID: 9, Process: "postgres", Loopback: true},
		{Proto: "tcp", Addr: "10.0.0.5", Port: 5432, PID: 9, Process: "postgres"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: 3000, PID: 11, Process: "docker-proxy"},
		{Proto: "tcp6", Addr: "::", Port: 3000, PID: 12, Process: "docker-proxy"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: 8080, Identity: Identity{Name: "http-alt"}},
		{Proto: "tcp6", Addr: "::", Port: 8080, Identity: Identity{Name: "http-alt"}},
	}
	g := group(rows, nil)
	if len(g) != 5 {
		t.Fatalf("got %d groups, want 5: %+v", len(g), g)
	}
	nmbd := g[0]
	if nmbd.Row.Addr != "0.0.0.0" || len(nmbd.Members) != 2 || !nmbd.Row.New {
		t.Errorf("nmbd leader: %+v", nmbd)
	}
	ssh := g[1]
	if ssh.Row.Proto != "tcp" || ssh.Row.Conn != 3 {
		t.Errorf("ssh leader: proto=%s conn=%d", ssh.Row.Proto, ssh.Row.Conn)
	}
	if pg := g[2]; pg.Row.Loopback {
		t.Error("group with a non-loopback member must not be dimmed as loopback")
	}
	if dp := g[3]; len(dp.Members) != 1 || dp.Members[0].PID == dp.Row.PID {
		t.Errorf("docker-proxy pair (different pids) should fold: %+v", dp)
	}
	if unk := g[4]; unk.Row.PID != 0 || len(unk.Members) != 1 {
		t.Errorf("pid-less rows should group by identity: %+v", unk)
	}
	// Expanding a key adds its members as lines right after the leader.
	e := group(rows, map[string]bool{groupKey(rows[1]): true})
	if len(e) != 7 || !e[1].Member || !e[2].Member || e[3].Member {
		t.Errorf("expanded shape wrong: %+v", e)
	}
	if len(flat(rows)) != 11 {
		t.Error("flat should keep every row")
	}
}

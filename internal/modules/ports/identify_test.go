package ports

import "testing"

func TestPortTableHasEnoughEntries(t *testing.T) {
	r, err := newResolver("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(r.ports.TCP) + len(r.ports.UDP); n < 150 {
		t.Errorf("port table has %d entries, HANDOFF wants ≥150", n)
	}
}

func TestResolveOrder(t *testing.T) {
	r, err := newResolver("", []byte("ssh 22/tcp\nfoo 4242/tcp # made up\n"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		l         Listener
		name, src string
		http      bool
	}{
		{Listener{Proto: "tcp", Port: 3000, Process: "docker-proxy"}, "container", SrcDocker, true},
		{Listener{Proto: "tcp", Port: 2222, Process: "sshd"}, "ssh", SrcProcess, false},
		{Listener{Proto: "tcp", Port: 80, Process: "nginx"}, "nginx", SrcProcess, true},
		{Listener{Proto: "tcp6", Port: 8096, Process: ""}, "jellyfin", SrcPortTable, true},
		{Listener{Proto: "udp", Port: 5353, Process: ""}, "mdns", SrcPortTable, false},
		{Listener{Proto: "tcp", Port: 4242, Process: ""}, "foo", SrcServices, false},
		{Listener{Proto: "tcp", Port: 4243, Process: "node"}, "? node — likely dev server", SrcHeuristic, true},
		{Listener{Proto: "tcp", Port: 36009, Process: "python3", Cmdline: "/usr/bin/python3 -P -m homeassistant --config /config"}, "? python3 -m homeassistant --config", SrcHeuristic, false},
		{Listener{Proto: "tcp", Port: 4243, Process: "weird"}, "? weird", SrcHeuristic, false},
		{Listener{Proto: "tcp", Port: 4243, Process: ""}, "?", SrcUnknown, false},
	}
	for _, c := range cases {
		got := r.Resolve(c.l)
		if got.Name != c.name || got.Source != c.src || got.HTTP != c.http {
			t.Errorf("%+v → %+v, want %s/%s http=%v", c.l, got, c.name, c.src, c.http)
		}
	}
}

func TestUserOverride(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/ports.toml", "[tcp]\n8096 = \"my-media\"\n"); err != nil {
		t.Fatal(err)
	}
	r, err := newResolver(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(Listener{Proto: "tcp", Port: 8096}); got.Name != "my-media" {
		t.Errorf("override ignored: %+v", got)
	}
}

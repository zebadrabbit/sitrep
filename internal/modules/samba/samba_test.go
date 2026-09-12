package samba

import (
	"context"
	"strings"
	"testing"
)

func TestParseTestparm(t *testing.T) {
	out := `[global]
	server string = x
[homes]
	browseable = No
	read only = No
[media]
	path = /srv/media
	guest ok = Yes
[scratch]
	path = /srv/scratch
	writable = yes
	browseable = yes
`
	g, sh := ParseTestparm([]byte(out))
	if g["server string"] != "x" {
		t.Errorf("global: %v", g)
	}
	if len(sh) != 3 {
		t.Fatalf("got %d shares", len(sh))
	}
	if sh[0].Name != "homes" || sh[0].Browseable || !sh[0].Writable {
		t.Errorf("homes: %+v", sh[0])
	}
	if sh[1].Path != "/srv/media" || !sh[1].Guest || sh[1].Writable || !sh[1].Browseable {
		t.Errorf("media: %+v", sh[1])
	}
	if !sh[2].Writable {
		t.Errorf("scratch: %+v", sh[2])
	}
	if _, sh := ParseTestparm(nil); len(sh) != 0 {
		t.Error("empty")
	}
}

func TestParseTestparmVerbose(t *testing.T) {
	out := `[global]
	server role = standalone server
	workgroup = WORKGROUP
	server min protocol = SMB2_02
	server max protocol = SMB3
	hosts allow = 
	panic action = /usr/share/samba/panic-action %d
[clippy]
	path = /srv/clippy
	valid users = clippy
	force user = user
	hosts allow = 127.0.0.1 10.0.1.0/24
	hosts deny = 0.0.0.0/0
`
	g, sh := ParseTestparm([]byte(out))
	if g["server role"] != "standalone server" || g["server max protocol"] != "SMB3" || g["hosts allow"] != "" {
		t.Errorf("global: %v", g)
	}
	if _, ok := g["panic action"]; ok {
		t.Error("only the allowlisted keys should be kept")
	}
	if sh[0].ValidUsers != "clippy" || sh[0].ForceUser != "user" || sh[0].HostsAllow != "127.0.0.1 10.0.1.0/24" || sh[0].HostsDeny != "0.0.0.0/0" {
		t.Errorf("share: %+v", sh[0])
	}
}

func TestDemoShowsServerBlock(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v := m.View(d, 100, 40)
	for _, want := range []string{"standalone", "WORKGROUP", "SMB3", "smbd", "USERS", "HOSTS"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestParseSmbstatus(t *testing.T) {
	out := `{"version":"4.19.5","sessions":{"1":{"username":"alice","remote_machine":"10.0.0.9","session_dialect":"SMB3_11"}},
"tcons":{"a":{"service":"media","session_id":"1","connected_at":"2026-09-10T15:18:11.037585-05:00"},"b":{"service":"IPC$","session_id":"1","connected_at":"2026-09-10T15:18:12-05:00"}},
"open_files":{"x":{},"y":{}}}`
	ss, files, ver, err := ParseSmbstatus([]byte(out))
	if err != nil || len(ss) != 1 || files != 2 || ver != "4.19.5" {
		t.Fatalf("ss=%+v files=%d ver=%s err=%v", ss, files, ver, err)
	}
	if ss[0].User != "alice" || len(ss[0].Shares) != 2 || ss[0].Since.IsZero() {
		t.Errorf("session: %+v", ss[0])
	}
	if _, _, _, err := ParseSmbstatus([]byte("{")); err == nil {
		t.Error("malformed must error")
	}
}

func TestEmbeddedFixture(t *testing.T) {
	m := New(true)
	d, err := m.Collect(context.Background())
	if err != nil {
		t.Skip("no samba fixture:", err)
	}
	sd := d.(Data)
	if len(sd.Shares) == 0 {
		t.Fatal("no shares in fixture")
	}
	if c := m.Card(d, 60); strings.Count(c, "\n") > 5 {
		t.Error("card too tall")
	}
	if v := m.View(d, 100, 0); !strings.Contains(v, "shares") {
		t.Error("view")
	}
}

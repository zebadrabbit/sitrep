package insight

import (
	"encoding/json"
	"testing"
)

func TestDiff(t *testing.T) {
	old := Snapshot{
		"system":   json.RawMessage(`{"kernel":"6.8.0-1","uptime":900000000000}`),
		"ports":    json.RawMessage(`{"rows":[{"proto":"tcp","addr":"0.0.0.0","port":22,"process":"sshd","identity":{"name":"ssh"}},{"proto":"tcp6","addr":"[::]","port":22,"process":"sshd","identity":{"name":"ssh"}},{"proto":"tcp","port":8080,"process":"python3","identity":{"name":"dev"}},{"proto":"tcp","port":80,"process":"nginx","identity":{"name":"http"}}]}`),
		"docker":   json.RawMessage(`{"containers":[{"name":"planka","image":"planka:1","state":"running"},{"name":"old","image":"x","state":"running"}]}`),
		"services": json.RawMessage(`{"units":[{"unit":"certbot.service","active":"active"}]}`),
		"disks":    json.RawMessage(`{"mounts":[{"target":"/","pct":50},{"target":"/mnt/usb","pct":10}]}`),
		"samba":    json.RawMessage(`{"error":"missing"}`),
	}
	cur := Snapshot{
		"system":   json.RawMessage(`{"kernel":"6.8.0-2","uptime":60000000000}`),
		"ports":    json.RawMessage(`{"rows":[{"proto":"tcp","addr":"0.0.0.0","port":22,"process":"sshd","identity":{"name":"ssh"}},{"proto":"tcp","port":3000,"process":"node","identity":{"name":"dev"}},{"proto":"tcp","port":80,"process":"caddy","identity":{"name":"http"}}]}`),
		"docker":   json.RawMessage(`{"containers":[{"name":"planka","image":"planka:1","state":"exited"}]}`),
		"services": json.RawMessage(`{"units":[{"unit":"certbot.service","active":"failed"}]}`),
		"disks":    json.RawMessage(`{"mounts":[{"target":"/","pct":58}]}`),
		"samba":    json.RawMessage(`{"shares":[]}`),
	}
	want := []Change{
		{"changed", "System", "kernel 6.8.0-1 → 6.8.0-2"},
		{"changed", "System", "rebooted (uptime reset)"},
		{"new", "Ports", "tcp :3000  dev (node)"},
		{"changed", "Ports", "tcp :80  nginx → caddy"},
		{"gone", "Ports", "tcp :8080  dev (python3)"},
		{"changed", "Docker", "planka  running → exited"},
		{"gone", "Docker", "old  x"},
		{"changed", "Services", "certbot.service  active → failed"},
		{"changed", "Disks", "/  50% → 58%"},
		{"gone", "Disks", "/mnt/usb unmounted"},
	}
	got := Diff(old, cur)
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if n := len(Diff(cur, cur)); n != 0 {
		t.Errorf("self-diff gave %d changes", n)
	}
}

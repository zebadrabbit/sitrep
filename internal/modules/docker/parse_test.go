package docker

import "testing"

func TestParsePS(t *testing.T) {
	out := `{"ID":"932613810e80","Names":"homeassistant","Image":"ghcr.io/home-assistant/home-assistant:stable","State":"running","Status":"Up 4 days","Ports":"","Labels":"com.docker.compose.project=homeassistant,com.docker.compose.service=ha","CreatedAt":"2026-09-06 17:20:36 -0500 CDT"}
{"ID":"abc","Names":"planka-planka-1","Image":"ghcr.io/plankanban/planka:2.1.1","State":"running","Status":"Up 3 days (healthy)","Ports":"0.0.0.0:3000->1337/tcp, [::]:3000->1337/tcp","Labels":"com.docker.compose.project=planka","CreatedAt":"x"}
{"ID":"def","Names":["/podman-style"],"Image":"redis:7","State":"exited","Status":"Exited (0) 2 hours ago","Ports":"6379/tcp","Labels":"","CreatedAt":"x"}
{"ID":"ghi","Names":"kong","Image":"kong/kong:3.9.1","State":"running","Status":"Up 3 days (unhealthy)","Ports":"8001-8004/tcp, 0.0.0.0:8010->8000/tcp, 0.0.0.0:8446->8443/tcp, 0.0.0.0:9000-9001->9000-9001/udp","Labels":"","CreatedAt":"x"}
not json
`
	cs := ParsePS([]byte(out))
	if len(cs) != 4 {
		t.Fatalf("got %d containers", len(cs))
	}
	if cs[0].Project != "homeassistant" || len(cs[0].Ports) != 0 || !cs[0].Running || cs[0].Health != "" {
		t.Errorf("host-network container: %+v", cs[0])
	}
	if p := cs[1]; p.Health != "healthy" || len(p.Ports) != 2 || p.Ports[0].HostPort != 3000 || p.Ports[0].ContainerPort != 1337 || p.Ports[1].HostIP != "::" {
		t.Errorf("planka: %+v", p)
	}
	if cs[2].Name != "podman-style" || cs[2].Running || cs[2].State != "exited" {
		t.Errorf("podman names array: %+v", cs[2])
	}
	if k := cs[3]; k.Health != "unhealthy" || len(k.Ports) != 4 || k.Ports[3].Proto != "udp" || k.Ports[3].HostPort != 9001 {
		t.Errorf("kong ranges: %+v", k.Ports)
	}
	if ShortImage("ghcr.io/plankanban/planka:2.1.1") != "planka" || ShortImage("redis@sha256:abc") != "redis" || ShortImage("supabase/postgres:17.6") != "postgres" {
		t.Error("ShortImage")
	}
	if len(ParsePS(nil)) != 0 {
		t.Error("empty")
	}
}

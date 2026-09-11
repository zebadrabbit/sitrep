// Package detect answers "what kind of box is this" once at startup.
package detect

import (
	"os"
	"os/exec"
	"strings"
)

// Env is passed to every module's Detect.
type Env struct {
	Root      bool
	Init      string // "systemd", "openrc", "runit", "sysv", "unknown"
	Appliance string // "" or "truenas", "proxmox", "unraid", "synology", "openwrt"
	Runtime   string // "" , "docker", "podman"
	Demo      bool
}

// Detect inspects the host. Cheap: a handful of stats and LookPaths.
func Detect() Env {
	return Env{
		Root:      os.Geteuid() == 0,
		Init:      initSystem(),
		Appliance: appliance(),
		Runtime:   runtime(),
	}
}

// Has reports whether a binary is on PATH.
func Has(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func initSystem() string {
	if b, err := os.ReadFile("/proc/1/comm"); err == nil {
		switch c := strings.TrimSpace(string(b)); c {
		case "systemd":
			return "systemd"
		case "init":
			if _, err := os.Stat("/run/openrc"); err == nil {
				return "openrc"
			}
			return "sysv"
		case "runit":
			return "runit"
		default:
			return c
		}
	}
	return "unknown"
}

// appliance markers per HANDOFF §4.3. Full gating lands in Phase 3.
func appliance() string {
	for _, p := range []struct{ path, name string }{
		{"/etc/pve", "proxmox"},
		{"/boot/config/ident.cfg", "unraid"},
		{"/etc/synoinfo.conf", "synology"},
		{"/etc/openwrt_release", "openwrt"},
	} {
		if _, err := os.Stat(p.path); err == nil {
			return p.name
		}
	}
	if b, err := os.ReadFile("/etc/version"); err == nil && strings.Contains(strings.ToLower(string(b)), "truenas") {
		return "truenas"
	}
	return ""
}

func runtime() string {
	switch {
	case Has("docker"):
		return "docker"
	case Has("podman"):
		return "podman"
	}
	return ""
}

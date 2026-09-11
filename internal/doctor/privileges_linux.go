//go:build linux

package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/zebadrabbit/sitrep/internal/detect"
)

func privileges() Section {
	cs := []Check{}
	uid := os.Geteuid()
	if uid == 0 {
		cs = append(cs, Check{OK, "euid", "0 (root) — full view"})
		return Section{"Privileges", cs}
	}
	if detect.Detect().Caps {
		cs = append(cs, Check{OK, "euid", fmt.Sprintf("%d — capabilities cover other users' pids", uid)})
		cs = append(cs, Check{OK, "capabilities", detect.WantCaps + " present"})
		return Section{"Privileges", cs}
	}
	exe, _ := os.Executable()
	cs = append(cs, Check{Degraded, "euid", fmt.Sprintf("%d — pids owned by other users show as ◐", uid)})
	cs = append(cs, Check{Degraded, "capabilities", "none — pids and fds of other users hidden"})
	cs = append(cs, Check{Degraded, "fix: setcap", fmt.Sprintf("sudo setcap %s+ep %s", detect.WantCaps, exe)})
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(exe, home+"/") {
		// sudo's secure_path drops ~/.local/bin, so `sudo sitrep` says command not found.
		cs = append(cs, Check{Degraded, "fix: sudo", "sudo " + exe + " doctor — binary is under $HOME, not on root's PATH"})
	}
	return Section{"Privileges", cs}
}

//go:build linux

package doctor

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Capabilities that let an unprivileged sitrep see other users' pids and
// read /proc/<pid>/fd, which is what the ◐ fields need. smbstatus and SMART
// still need real root.
const wantCaps = "cap_sys_ptrace,cap_dac_read_search"

const (
	capDACReadSearch = 2
	capSysPtrace     = 19
)

func privileges() Section {
	cs := []Check{}
	uid := os.Geteuid()
	if uid == 0 {
		cs = append(cs, Check{OK, "euid", "0 (root) — full view"})
		return Section{"Privileges", cs}
	}
	cs = append(cs, Check{Degraded, "euid", fmt.Sprintf("%d — pids owned by other users show as ◐", uid)})
	eff := effectiveCaps()
	has := eff&(1<<capSysPtrace) != 0 && eff&(1<<capDACReadSearch) != 0
	if has {
		cs = append(cs, Check{OK, "capabilities", wantCaps + " present"})
		return Section{"Privileges", cs}
	}
	exe, _ := os.Executable()
	cs = append(cs, Check{Degraded, "capabilities", "none — pids and fds of other users hidden"})
	cs = append(cs, Check{Degraded, "fix: setcap", fmt.Sprintf("sudo setcap %s+ep %s", wantCaps, exe)})
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(exe, home+"/") {
		// sudo's secure_path drops ~/.local/bin, so `sudo sitrep` says command not found.
		cs = append(cs, Check{Degraded, "fix: sudo", "sudo " + exe + " doctor — binary is under $HOME, not on root's PATH"})
	}
	return Section{"Privileges", cs}
}

// effectiveCaps reads CapEff from /proc/self/status. 0 on any failure.
func effectiveCaps() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "CapEff:"); ok {
			n, _ := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			return n
		}
	}
	return 0
}

//go:build linux

package collect

import (
	"bufio"
	"bytes"
	"fmt"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// ClockTicks is CLK_TCK. Linux userspace ABI fixes it at 100 (sysconf would
// need cgo, which we don't have).
const ClockTicks = 100

// BootTime reads btime from /proc/stat.
func (r *Runner) BootTime() (time.Time, error) {
	b, err := r.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return time.Unix(n, 0), err
		}
	}
	return time.Time{}, fmt.Errorf("proc: no btime in /proc/stat")
}

// ProcStart returns when pid started, from field 22 of /proc/<pid>/stat.
func (r *Runner) ProcStart(pid int, boot time.Time) (time.Time, error) {
	b, err := r.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, err
	}
	// comm may contain spaces; everything after the last ')' is positional.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return time.Time{}, fmt.Errorf("proc: malformed stat for %d", pid)
	}
	f := strings.Fields(string(b[i+1:]))
	// fields after ')' start at stat field 3, so starttime (22) is index 19.
	if len(f) < 20 {
		return time.Time{}, fmt.Errorf("proc: short stat for %d", pid)
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return boot.Add(time.Duration(ticks) * time.Second / ClockTicks), nil
}

// ProcUID returns the real uid from /proc/<pid>/status.
func (r *Runner) ProcUID(pid int) (int, error) {
	b, err := r.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "Uid:"); ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				return strconv.Atoi(f[0])
			}
		}
	}
	return -1, fmt.Errorf("proc: no Uid for %d", pid)
}

// ProcCmdline returns argv joined by spaces.
func (r *Runner) ProcCmdline(pid int) string {
	b, err := r.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}

// ProcCgroup returns the systemd unit or container hint from /proc/<pid>/cgroup.
func (r *Runner) ProcCgroup(pid int) string {
	b, err := r.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	if i := strings.LastIndex(line, "/"); i >= 0 {
		return line[i+1:]
	}
	return line
}

// Username resolves a uid, falling back to the number.
func Username(uid int) string {
	if uid < 0 {
		return ""
	}
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return strconv.Itoa(uid)
}

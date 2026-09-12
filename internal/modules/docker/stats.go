package docker

import (
	"strconv"
	"strings"
	"time"
)

// cgroupPatterns are where a container's cgroup v2 directory lives, by
// runtime and cgroup driver: docker+systemd, docker+cgroupfs, podman.
// %s is the container id (a prefix is enough; the glob completes it).
var cgroupPatterns = []string{
	"/sys/fs/cgroup/system.slice/docker-%s*.scope",
	"/sys/fs/cgroup/docker/%s*",
	"/sys/fs/cgroup/machine.slice/libpod-%s*.scope",
}

// stats fills CPU% and memory per running container from cgroup v2, no
// exec: `docker stats` blocks for a full sampling interval (2s here) which
// would make every tick slow. CPU% is the usage_usec delta since the last
// tick, so it is blank on the first one, like docker stats' own first frame.
func (m *Module) stats(cs []Container, now time.Time) {
	dt := now.Sub(m.statsAt).Seconds()
	cur := map[string]uint64{}
	for i := range cs {
		c := &cs[i]
		if !c.Running {
			continue
		}
		dir := m.cgroupDir(c.ID)
		if dir == "" {
			continue
		}
		if b, err := m.run.ReadFile(dir + "/cpu.stat"); err == nil {
			usage := ParseCPUStat(b)
			cur[c.ID] = usage
			if prev, ok := m.prevCPU[c.ID]; ok && dt > 0 && usage >= prev {
				c.CPUPct = float64(usage-prev) / (dt * 1e6) * 100
				c.HasCPU = true
			}
		}
		if b, err := m.run.ReadFile(dir + "/memory.current"); err == nil {
			c.Mem, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
			c.HasMem = true
		}
		if b, err := m.run.ReadFile(dir + "/memory.max"); err == nil {
			c.MemLimit, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64) // "max" → 0
		}
	}
	m.prevCPU, m.statsAt = cur, now
}

func (m *Module) cgroupDir(id string) string {
	for _, pat := range cgroupPatterns {
		if hits := m.run.Glob(strings.Replace(pat, "%s", id, 1)); len(hits) > 0 {
			return hits[0]
		}
	}
	return ""
}

// ParseCPUStat returns usage_usec from a cgroup v2 cpu.stat.
func ParseCPUStat(b []byte) uint64 {
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

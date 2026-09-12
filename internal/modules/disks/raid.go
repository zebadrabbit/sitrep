package disks

import (
	"strconv"
	"strings"
	"time"
)

// Array is one md device from /proc/mdstat.
type Array struct {
	Name     string   `json:"name"`
	Level    string   `json:"level"` // raid1, raid5, …
	State    string   `json:"state"` // active, inactive
	Members  []string `json:"members"`
	Status   string   `json:"status"`             // "[UU]" or "[U_]"
	Degraded bool     `json:"degraded"`           // any "_" in Status
	Progress string   `json:"progress,omitempty"` // "resync = 12.3%" while rebuilding
}

// Pool is one zpool from `zpool list -H`.
type Pool struct {
	Name   string `json:"name"`
	Health string `json:"health"` // ONLINE, DEGRADED, FAULTED, …
	Size   string `json:"size"`
	Alloc  string `json:"alloc"`
	Cap    string `json:"cap"` // "33%"
}

// ParseMdstat reads /proc/mdstat. The format is two lines per array:
//
//	md0 : active raid1 sdb1[1] sda1[0]
//	      1953382400 blocks super 1.2 [2/2] [UU]
//	      [==>..................]  resync = 12.3% (…) finish=…
func ParseMdstat(out []byte) []Array {
	var arrays []Array
	var cur *Array
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) >= 3 && strings.HasPrefix(f[0], "md") && f[1] == ":":
			arrays = append(arrays, Array{Name: f[0], State: f[2]})
			cur = &arrays[len(arrays)-1]
			for _, m := range f[3:] {
				if strings.HasPrefix(m, "raid") || m == "linear" || m == "multipath" {
					cur.Level = m
					continue
				}
				if i := strings.Index(m, "["); i > 0 {
					m = m[:i] // sda1[0](F) → sda1
				}
				cur.Members = append(cur.Members, m)
			}
		case cur != nil && len(f) > 0 && strings.HasPrefix(f[len(f)-1], "[") && strings.Contains(f[len(f)-1], "U"):
			cur.Status = f[len(f)-1]
			cur.Degraded = strings.Contains(cur.Status, "_")
		case cur != nil && len(f) >= 3 && f[1] == "=" && strings.HasSuffix(f[2], "%"):
			cur.Progress = f[0] + " " + f[2] // "resync 12.3%"
		case cur != nil && len(f) >= 4 && (f[1] == "resync" || f[1] == "recovery" || f[1] == "reshape" || f[1] == "check") && f[2] == "=":
			cur.Progress = f[1] + " " + f[3]
		}
	}
	return arrays
}

// ParseZpoolList reads `zpool list -H -o name,health,size,alloc,cap`.
func ParseZpoolList(out []byte) []Pool {
	var pools []Pool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 {
			pools = append(pools, Pool{Name: f[0], Health: f[1], Size: f[2], Alloc: f[3], Cap: f[4]})
		}
	}
	return pools
}

// diskstat is the cumulative counters of one device from /proc/diskstats:
// sectors read, sectors written, ms spent doing I/O.
type diskstat struct{ rd, wr, busy uint64 }

// ParseDiskstats keeps whole disks and md/dm devices; partitions and loops
// are noise here.
func ParseDiskstats(out []byte) map[string]diskstat {
	m := map[string]diskstat{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 14 {
			continue
		}
		name := f[2]
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
			continue
		}
		rd, _ := strconv.ParseUint(f[5], 10, 64)
		wr, _ := strconv.ParseUint(f[9], 10, 64)
		busy, _ := strconv.ParseUint(f[12], 10, 64)
		m[name] = diskstat{rd, wr, busy}
	}
	return m
}

// rates turns two diskstats samples dt apart into bytes/s and util% per
// device, annotating the tree in place (children too: md and dm devices
// hang under disks in lsblk).
func rates(devs []Device, prev, cur map[string]diskstat, dt time.Duration) {
	if dt <= 0 {
		return
	}
	sec := dt.Seconds()
	for i := range devs {
		d := &devs[i]
		// Partitions share the disk's spindle; their counters just split the
		// same number. Match by kernel name: lsblk shows vg-lv, diskstats dm-0.
		if c, ok := cur[d.KName]; ok && d.Type != "part" {
			if p, ok := prev[d.KName]; ok && c.rd >= p.rd && c.wr >= p.wr && c.busy >= p.busy {
				d.RdRate = float64(c.rd-p.rd) * 512 / sec
				d.WrRate = float64(c.wr-p.wr) * 512 / sec
				d.Util = min(100, float64(c.busy-p.busy)/(sec*1000)*100)
				d.HasIO = true
			}
		}
		rates(d.Children, prev, cur, dt)
	}
}

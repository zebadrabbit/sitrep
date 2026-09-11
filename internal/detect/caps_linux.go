//go:build linux

package detect

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
	"strings"
)

// hasCaps reports whether the effective set carries WantCaps.
func hasCaps() bool {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	return capsOf(b)&wantMask == wantMask
}

// capsOf parses CapEff from /proc/<pid>/status. 0 on any failure.
func capsOf(status []byte) uint64 {
	sc := bufio.NewScanner(bytes.NewReader(status))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "CapEff:"); ok {
			n, _ := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			return n
		}
	}
	return 0
}

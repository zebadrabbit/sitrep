package updates

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Pkg is one line of `apt list --upgradable`.
type Pkg struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Source   string `json:"source"`
	Arch     string `json:"arch"`
	Security bool   `json:"security"`
}

// base-files/noble-updates 13ubuntu10.5 amd64 [upgradable from: 13ubuntu10.4]
var aptRe = regexp.MustCompile(`^(\S+?)/(\S+) (\S+) (\S+) \[upgradable from: ([^\]]+)\]`)

// ParseAptList skips the "Listing..." header and anything it can't read.
func ParseAptList(out []byte) []Pkg {
	var pkgs []Pkg
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := aptRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		pkgs = append(pkgs, Pkg{Name: m[1], Source: m[2], To: m[3], Arch: m[4], From: m[5], Security: strings.Contains(m[2], "security")})
	}
	return pkgs
}

// Step is one "--- name ---" section of an update-all.sh run.
type Step struct {
	Name   string   `json:"name"`
	Failed []string `json:"failed,omitempty"`
}

// Run is one "===== ts =====" … "done ts" block.
type Run struct {
	Started        time.Time `json:"started"`
	Finished       time.Time `json:"finished"`
	Complete       bool      `json:"complete"`
	Steps          []Step    `json:"steps"`
	Failures       int       `json:"failures"`
	RebootRequired bool      `json:"reboot_required"`
}

// ParseLog returns every run in the log, oldest first. The trailing summary
// step repeats the failure lines, so it is not counted twice.
func ParseLog(out []byte) []Run {
	var runs []Run
	var cur *Run
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "===== ") && strings.HasSuffix(line, " ====="):
			runs = append(runs, Run{})
			cur = &runs[len(runs)-1]
			cur.Started, _ = time.Parse(time.RFC3339, strings.Trim(line, "= "))
		case cur == nil:
			continue
		case strings.HasPrefix(line, "--- ") && strings.HasSuffix(line, " ---"):
			cur.Steps = append(cur.Steps, Step{Name: strings.Trim(line, "- ")})
		case strings.HasPrefix(line, "!! failed: "):
			if n := len(cur.Steps); n > 0 && cur.Steps[n-1].Name != "summary" {
				cur.Steps[n-1].Failed = append(cur.Steps[n-1].Failed, strings.TrimPrefix(line, "!! failed: "))
				cur.Failures++
			}
		case strings.HasPrefix(line, "** REBOOT REQUIRED"):
			cur.RebootRequired = true
		case strings.HasPrefix(line, "done "):
			cur.Finished, _ = time.Parse(time.RFC3339, strings.TrimPrefix(line, "done "))
			cur.Complete = true
		}
	}
	return runs
}

// kernelVersion extracts "6.8.0-139-generic" from /boot/vmlinuz-6.8.0-139-generic.
func kernelVersion(path string) string {
	if i := strings.Index(path, "vmlinuz-"); i >= 0 {
		return path[i+len("vmlinuz-"):]
	}
	return ""
}

var numRe = regexp.MustCompile(`\d+`)

// newerKernel compares numeric fields: 6.8.0-139 > 6.8.0-138 > 6.7.9-200.
func newerKernel(a, b string) bool {
	na, nb := numRe.FindAllString(a, -1), numRe.FindAllString(b, -1)
	for i := 0; i < len(na) && i < len(nb); i++ {
		x, _ := strconv.Atoi(na[i])
		y, _ := strconv.Atoi(nb[i])
		if x != y {
			return x > y
		}
	}
	return len(na) > len(nb)
}

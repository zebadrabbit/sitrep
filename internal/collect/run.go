// Package collect is the only place sitrep executes commands or reads host
// files. It applies timeouts, records durations for doctor, captures
// fixtures when SITREP_CAPTURE_FIXTURES=1, and serves fixtures in --demo.
package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	sitrep "github.com/zebadrabbit/sitrep"
)

// ErrMissing means the binary is not on PATH.
var ErrMissing = errors.New("not installed")

// ErrNoFixture means demo mode has nothing for this command.
var ErrNoFixture = errors.New("no fixture")

// Runner is one module's handle. Demo and capture are per-runner so tests
// can build one against a temp dir.
type Runner struct {
	Module     string
	Demo       bool
	CaptureDir string // "" = no capture; else testdata/fixtures root
	fixtures   fs.FS  // demo source; defaults to the embedded set
}

// New builds a Runner for a module. Demo and capture come from the env.
func New(module string, demo bool) *Runner {
	r := &Runner{Module: module, Demo: demo, fixtures: sitrep.Fixtures}
	if os.Getenv("SITREP_CAPTURE_FIXTURES") == "1" {
		r.CaptureDir = "testdata/fixtures"
	}
	return r
}

// WithFixtures overrides the demo source (tests).
func (r *Runner) WithFixtures(f fs.FS) *Runner { r.fixtures = f; return r }

// Result is what an exec produced.
type Result struct {
	Stdout, Stderr []byte
	Took           time.Duration
}

// Run executes name args… under ctx. In demo it returns the fixture instead.
func (r *Runner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	key := FixtureName(name, args...)
	if r.Demo {
		b, err := r.fixture(key)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %s: %w", r.Module, key, ErrNoFixture)
		}
		return Result{Stdout: b}, nil
	}
	if _, err := exec.LookPath(name); err != nil {
		return Result{}, fmt.Errorf("%s: %s: %w", r.Module, name, ErrMissing)
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), Took: time.Since(start)}
	if ctx.Err() != nil {
		return res, fmt.Errorf("%s: %s: %w", r.Module, name, ctx.Err())
	}
	if err != nil {
		return res, fmt.Errorf("%s: %s: %w: %s", r.Module, name, err, strings.TrimSpace(errb.String()))
	}
	r.capture(key, res.Stdout)
	return res, nil
}

// ReadFile reads a host file (/proc, /etc). In demo it reads
// fixtures/<module>/fs/<path>.
func (r *Runner) ReadFile(path string) ([]byte, error) {
	key := "fs" + path
	if r.Demo {
		return r.fixture(key)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r.capture(key, b)
	return b, nil
}

// Readlink resolves a symlink (/proc/<pid>/exe, cwd). Demo reads a fixture
// holding the target path.
func (r *Runner) Readlink(path string) (string, error) {
	key := "fs" + path + ".link"
	if r.Demo {
		b, err := r.fixture(key)
		return strings.TrimSpace(string(b)), err
	}
	s, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	r.capture(key, []byte(s))
	return s, nil
}

// Glob lists paths (/proc/[0-9]*). Demo lists the fixture tree.
func (r *Runner) Glob(pattern string) []string {
	if r.Demo {
		base := "testdata/fixtures/" + r.Module + "/fs"
		matches, _ := fs.Glob(r.fixtures, base+pattern)
		for i, m := range matches {
			matches[i] = strings.TrimPrefix(m, base)
		}
		return matches
	}
	m, _ := filepath.Glob(pattern)
	return m
}

// FixtureName maps a command line to a fixture file: "ss -tulnpH" → "ss_tulnpH.txt".
func FixtureName(name string, args ...string) string {
	if len(args) == 0 {
		return name + ".txt"
	}
	clean := strings.NewReplacer("-", "", "/", "_", " ", "_", "=", "_", "{", "", "}", "", "'", "", ".", "").Replace(strings.Join(args, "_"))
	return name + "_" + clean + ".txt"
}

func (r *Runner) fixture(key string) ([]byte, error) {
	return fs.ReadFile(r.fixtures, "testdata/fixtures/"+r.Module+"/"+key)
}

// Capture writes a synthetic fixture (data a module derived without a
// command, like statfs results) under fs/<name>. No-op unless capturing.
func (r *Runner) Capture(name string, b []byte) { r.capture("fs"+name, b) }

func (r *Runner) capture(key string, b []byte) {
	if r.CaptureDir == "" {
		return
	}
	p := filepath.Join(r.CaptureDir, r.Module, key)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o644)
}

// ModTime stats a host file. Demo reads fixtures/<module>/fs/<path>.mtime
// holding an RFC 3339 timestamp; capture writes it.
func (r *Runner) ModTime(path string) (time.Time, error) {
	key := "fs" + path + ".mtime"
	if r.Demo {
		b, err := r.fixture(key)
		if err != nil {
			return time.Time{}, err
		}
		return time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	}
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	if !fi.Mode().IsRegular() {
		return time.Time{}, fmt.Errorf("%s: not a regular file", path)
	}
	r.capture(key, []byte(fi.ModTime().UTC().Format(time.RFC3339)))
	return fi.ModTime(), nil
}

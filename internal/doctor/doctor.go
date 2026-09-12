// Package doctor runs the `sitrep doctor` checklist (HANDOFF §8).
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/ui"
)

// State of one check. Fail makes the exit code non-zero.
type State int

const (
	OK State = iota
	Degraded
	Fail
)

// Check is one line of the report.
type Check struct {
	State  State  `json:"state"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// Section groups checks under a heading.
type Section struct {
	Title  string  `json:"title"`
	Checks []Check `json:"checks"`
}

// Report is the whole checklist.
type Report struct {
	Sections []Section `json:"sections"`
}

// Failed is true when any check is Fail.
func (r Report) Failed() bool {
	for _, s := range r.Sections {
		for _, c := range s.Checks {
			if c.State == Fail {
				return true
			}
		}
	}
	return false
}

// Run assembles every section (HANDOFF §8).
func Run(ctx context.Context, env detect.Env, entries []module.Entry) Report {
	env0 := environment()
	env0.Checks = append(env0.Checks, coldStart())
	mods, data := modulesSection(ctx, entries)
	return Report{Sections: []Section{env0, privileges(), configSection(), detection(env, entries), mods, portsSection(data["ports"])}}
}

// Write prints the report as text or JSON.
func (r Report) Write(w io.Writer, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	g := theme.Current().Glyph
	glyph := map[State]string{OK: g.OK, Degraded: g.Degraded, Fail: g.Fail}
	for _, s := range r.Sections {
		fmt.Fprintln(w, theme.Current().Bold.Render(s.Title))
		for _, c := range s.Checks {
			line := fmt.Sprintf("  %s %-28s %s", ui.Check(glyph[c.State]), c.Label, c.Detail)
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
	return nil
}

func environment() Section {
	var cs []Check
	if runtime.GOOS == "linux" {
		cs = append(cs, Check{OK, "os", runtime.GOOS + "/" + runtime.GOARCH})
	} else {
		cs = append(cs, Check{Fail, "os", runtime.GOOS + " is not supported; sitrep needs Linux"})
	}
	if _, err := os.Stat("/proc/self/stat"); err == nil {
		cs = append(cs, Check{OK, "/proc", "mounted"})
	} else {
		cs = append(cs, Check{Fail, "/proc", "not readable; nothing to collect"})
	}
	cs = append(cs, terminalCheck(), colorCheck(), localeCheck())
	return Section{"Environment", cs}
}

func terminalCheck() Check {
	t := os.Getenv("TERM")
	if !term.IsTerminal(os.Stdout.Fd()) {
		return Check{Degraded, "terminal", "stdout is not a tty (TERM=" + t + ")"}
	}
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return Check{Degraded, "terminal", "size unknown (TERM=" + t + ")"}
	}
	d := fmt.Sprintf("%dx%d TERM=%s", w, h, t)
	switch {
	case w < 80 || h < 24:
		return Check{Degraded, "terminal", d + " — below 80x24, even lite mode will clip"}
	case w < 100 || h < 30:
		return Check{OK, "terminal", d + " — lite layout (full needs 100x30)"}
	}
	return Check{OK, "terminal", d + " — full layout"}
}

func colorCheck() Check {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return Check{OK, "color", "not a tty; colors stripped"}
	}
	p := colorprofile.Detect(os.Stdout, os.Environ())
	switch p {
	case colorprofile.TrueColor:
		return Check{OK, "color", p.String()}
	case colorprofile.ANSI256:
		// ssh drops COLORTERM, so a truecolor emulator lands here and every
		// hex theme is snapped to the 256 cube (nord's green turns khaki).
		return Check{Degraded, "color", "256 colors; hex themes are quantized — export COLORTERM=truecolor if your emulator supports it, or use theme \"terminal\""}
	case colorprofile.ANSI:
		return Check{OK, "color", "16 colors; theme will be downsampled"}
	}
	return Check{Degraded, "color", p.String() + " — set theme = \"mono\" in config"}
}

func localeCheck() Check {
	for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if s := os.Getenv(v); s != "" {
			if strings.Contains(strings.ToUpper(s), "UTF-8") || strings.Contains(strings.ToUpper(s), "UTF8") {
				return Check{OK, "locale", v + "=" + s}
			}
			return Check{Degraded, "locale", v + "=" + s + " — not UTF-8; set ascii = true in theme"}
		}
	}
	return Check{Degraded, "locale", "no LANG/LC_* set; glyphs may not render — set ascii = true in theme"}
}

func configSection() Section {
	p := config.Path()
	cfg, unknown, err := config.LoadFrom(p)
	var cs []Check
	switch {
	case err != nil:
		cs = append(cs, Check{Fail, "config", err.Error()})
	case !exists(p):
		cs = append(cs, Check{OK, "config", p + " (absent, using defaults; `sitrep config init` to create)"})
	default:
		cs = append(cs, Check{OK, "config", p})
	}
	if len(unknown) > 0 {
		cs = append(cs, Check{Degraded, "unknown keys", strings.Join(unknown, ", ")})
	}
	if _, err := theme.Build(cfg.Theme, config.ThemeDir()); err != nil {
		cs = append(cs, Check{Fail, "theme", err.Error()})
	} else {
		cs = append(cs, Check{OK, "theme", cfg.Theme})
	}
	acks := "none"
	if len(cfg.Acks) > 0 {
		acks = strings.Join(cfg.Acks, ", ")
	}
	cs = append(cs, Check{OK, "acks", acks})
	return Section{"Config", cs}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

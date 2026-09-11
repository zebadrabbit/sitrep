// Package theme is the only place colors are defined. Views take styles from
// here; nothing else may construct a lipgloss.Color (HANDOFF §10.5).
package theme

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
)

//go:embed themes/*.toml
var builtin embed.FS

// Theme is the on-disk shape of a theme.toml.
type Theme struct {
	Name   string `toml:"name"`
	Accent string `toml:"accent"`
	OK     string `toml:"ok"`
	Warn   string `toml:"warn"`
	Crit   string `toml:"crit"`
	Dim    string `toml:"dim"`
	ASCII  bool   `toml:"ascii"`
}

// Glyphs is the complete allowed status glyph set (HANDOFF §7).
type Glyphs struct {
	OK, Fail, Degraded, Warn, Crit, New, Sep string
}

var unicodeGlyphs = Glyphs{OK: "●", Fail: "○", Degraded: "◐", Warn: "!", Crit: "!!", New: "+", Sep: "▸"}
var asciiGlyphs = Glyphs{OK: "*", Fail: "o", Degraded: "~", Warn: "!", Crit: "!!", New: "+", Sep: ">"}

// Styles is what views consume.
type Styles struct {
	Name string
	// Accent is the wordmark and selection color.
	Accent, OK, Warn, Crit, Dim, Bold lipgloss.Style
	// Hotkey is the one style for hotkey characters, everywhere (HANDOFF §10.6).
	Hotkey lipgloss.Style
	Glyph  Glyphs
}

var (
	mu      sync.RWMutex
	current = mustBuild("amber")
)

// Current returns the active style set.
func Current() Styles {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Set loads a theme by name (builtin or from dir) and makes it current.
func Set(name, dir string) error {
	s, err := Build(name, dir)
	if err != nil {
		return err
	}
	mu.Lock()
	current = s
	mu.Unlock()
	return nil
}

// Build resolves name to styles. A user file in dir wins over the builtin.
func Build(name, dir string) (Styles, error) {
	t, err := load(name, dir)
	if err != nil {
		return Styles{}, err
	}
	return t.styles(), nil
}

// List returns builtin theme names plus any *.toml in dir.
func List(dir string) []string {
	seen := map[string]bool{}
	ents, _ := fs.ReadDir(builtin, "themes")
	for _, e := range ents {
		seen[strings.TrimSuffix(e.Name(), ".toml")] = true
	}
	if dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		for _, f := range files {
			seen[strings.TrimSuffix(filepath.Base(f), ".toml")] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func load(name, dir string) (Theme, error) {
	var t Theme
	var data []byte
	var err error
	if dir != "" {
		data, err = os.ReadFile(filepath.Join(dir, name+".toml"))
	}
	if dir == "" || err != nil {
		data, err = builtin.ReadFile("themes/" + name + ".toml")
		if err != nil {
			return t, fmt.Errorf("theme %q not found", name)
		}
	}
	if _, err := toml.Decode(string(data), &t); err != nil {
		return t, fmt.Errorf("theme %s: %w", name, err)
	}
	if t.Name == "" {
		t.Name = name
	}
	return t, nil
}

func mustBuild(name string) Styles {
	s, err := Build(name, "")
	if err != nil {
		panic(err)
	}
	return s
}

func (t Theme) styles() Styles {
	s := Styles{Name: t.Name, Glyph: unicodeGlyphs}
	if t.ASCII {
		s.Glyph = asciiGlyphs
	}
	s.Accent = fg(t.Accent).Bold(true)
	s.OK = fg(t.OK)
	s.Warn = fg(t.Warn)
	s.Crit = fg(t.Crit).Bold(true)
	s.Dim = fg(t.Dim)
	if t.Dim == "" {
		s.Dim = s.Dim.Faint(true)
	}
	s.Bold = lipgloss.NewStyle().Bold(true)
	s.Hotkey = fg(t.Accent).Bold(true).Underline(t.Accent == "")
	return s
}

// fg builds a foreground style; empty hex means "terminal default".
func fg(hex string) lipgloss.Style {
	st := lipgloss.NewStyle()
	if hex == "" {
		return st
	}
	return st.Foreground(lipgloss.Color(hex))
}

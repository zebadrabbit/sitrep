// Package config owns ~/.config/sitrep/config.toml: load, init, show, acks.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk shape. Zero value = defaults.
type Config struct {
	Theme        string   `toml:"theme"`
	Enabled      []string `toml:"enabled"`       // empty = every detected module
	Disabled     []string `toml:"disabled"`      // explicit opt-outs
	Acks         []string `toml:"acks"`          // appliance_sensitive modules the owner has acknowledged
	DenseModules []string `toml:"dense_modules"` // --view dense box order
	Layout       string   `toml:"layout"`        // "" | "full" | "lite"
}

// Default is what `config init` writes and what a missing file means.
func Default() Config {
	return Config{
		Theme:        "amber",
		DenseModules: []string{"ports", "system", "services", "network"},
	}
}

// Dir is $XDG_CONFIG_HOME/sitrep or ~/.config/sitrep.
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "sitrep")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "sitrep")
	}
	return filepath.Join(home, ".config", "sitrep")
}

// Path is the config file location.
func Path() string { return filepath.Join(Dir(), "config.toml") }

// ThemeDir is where user theme overrides live.
func ThemeDir() string { return filepath.Join(Dir(), "themes") }

// Load reads Path(). A missing file is not an error: defaults apply.
// Unknown keys are returned so doctor can warn about them.
func Load() (Config, []string, error) {
	return LoadFrom(Path())
}

// LoadFrom is Load for an explicit path (tests, doctor).
func LoadFrom(path string) (cfg Config, unknown []string, err error) {
	cfg = Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil, nil
	}
	if err != nil {
		return cfg, nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return cfg, nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	for _, k := range md.Undecoded() {
		unknown = append(unknown, k.String())
	}
	if cfg.Theme == "" {
		cfg.Theme = "amber"
	}
	return cfg, unknown, nil
}

// Init writes the default config unless one exists.
func Init() (string, error) {
	p := Path()
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("config: %s already exists", p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return p, fmt.Errorf("config: mkdir: %w", err)
	}
	return p, Default().Save(p)
}

// Save writes cfg to path with a header comment.
func (c Config) Save(path string) error {
	var buf bytes.Buffer
	buf.WriteString("# sitrep configuration. Edit by hand or via `sitrep config` / `sitrep modules`.\n")
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// String renders the config as TOML for `config show`.
func (c Config) String() string {
	var buf bytes.Buffer
	_ = toml.NewEncoder(&buf).Encode(c)
	return strings.TrimSpace(buf.String())
}

// Acked reports whether the owner has acknowledged an appliance-sensitive module.
func (c Config) Acked(id string) bool {
	for _, a := range c.Acks {
		if a == id {
			return true
		}
	}
	return false
}

package module

import (
	"context"

	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
)

// Entry is a registered module with its runtime state.
type Entry struct {
	Module Module
	Hotkey rune // '1'..'9','0' over enabled modules, in registry order
	Avail  Availability
	// Enabled is false when disabled in config, needs ack, or is missing.
	Enabled bool
}

var registry []Module

// Register appends in display order. Called from internal/modules/registry.go.
func Register(m Module) { registry = append(registry, m) }

// All returns every compiled-in module in order.
func All() []Module { return registry }

// Resolve runs Detect on every module and applies config to produce the
// enabled set with hotkeys assigned. Overview is always first and always on.
func Resolve(ctx context.Context, env detect.Env, cfg config.Config) []Entry {
	entries := make([]Entry, 0, len(registry))
	hot := []rune("1234567890")
	n := 0
	for _, m := range registry {
		e := Entry{Module: m, Avail: m.Detect(ctx, env)}
		e.Enabled = enabled(m, e.Avail, env, cfg)
		if e.Enabled && n < len(hot) {
			e.Hotkey = hot[n]
			n++
		}
		entries = append(entries, e)
	}
	return entries
}

func enabled(m Module, a Availability, env detect.Env, cfg config.Config) bool {
	id := m.ID()
	for _, d := range cfg.Disabled {
		if d == id {
			return false
		}
	}
	if len(cfg.Enabled) > 0 && !contains(cfg.Enabled, id) {
		return false
	}
	if m.Flags().ApplianceSensitive && env.Appliance != "" && !cfg.Acked(id) {
		return false
	}
	switch a.State {
	case Missing, Unsupported, NeedsAck:
		return false
	}
	return true
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Lookup finds a module by id.
func Lookup(id string) (Module, bool) {
	for _, m := range registry {
		if m.ID() == id {
			return m, true
		}
	}
	return nil, false
}

// OneShotOrder returns entries reordered so every module's After deps come
// first. Display order is otherwise preserved. Cycles are not handled: don't
// make any.
func OneShotOrder(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	done := map[string]bool{}
	var add func(e Entry)
	add = func(e Entry) {
		id := e.Module.ID()
		if done[id] {
			return
		}
		done[id] = true
		for _, dep := range e.Module.Flags().After {
			for _, x := range entries {
				if x.Module.ID() == dep {
					add(x)
				}
			}
		}
		out = append(out, e)
	}
	for _, e := range entries {
		add(e)
	}
	return out
}

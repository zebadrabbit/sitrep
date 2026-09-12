package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/module"
)

// slot is the per-module store entry.
type slot struct {
	data       module.Data
	err        error
	took       time.Duration
	updated    time.Time
	collecting bool
}

type tickMsg struct{ id string }

// collectCmd runs one collection off the UI goroutine with the module's deadline.
func collectCmd(m module.Module) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), module.Timeout(m))
		defer cancel()
		start := time.Now()
		d, err := m.Collect(ctx)
		return module.DataMsg{ID: m.ID(), Data: d, Err: err, Took: time.Since(start)}
	}
}

// tickCmd schedules the next collection at the module's interval. Never
// faster than 1s (HANDOFF §10.2).
func tickCmd(m module.Module) tea.Cmd {
	iv := max(m.Interval(), time.Second)
	return tea.Tick(iv, func(time.Time) tea.Msg { return tickMsg{id: m.ID()} })
}

// startAll kicks off the first collection for every enabled module.
func (m Model) startAll() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.entries))
	for _, e := range m.entries {
		if e.Enabled && e.Module.Interval() > 0 {
			m.store[e.Module.ID()].collecting = true
			cmds = append(cmds, collectCmd(e.Module))
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) onData(msg module.DataMsg) (tea.Model, tea.Cmd) {
	s := m.store[msg.ID]
	if s == nil {
		return m, nil
	}
	s.collecting = false
	if m.frozen {
		// f: drop the result and stop the ticker; unfreezing restarts everything.
		return m, nil
	}
	s.took = msg.Took
	s.updated = time.Now()
	s.err = msg.Err
	if msg.Err == nil {
		// A failed collection keeps the previous data on screen (HANDOFF §4.1).
		s.data = msg.Data
	}
	mod, _ := module.Lookup(msg.ID)
	return m, tickCmd(mod)
}

func (m Model) onTick(msg tickMsg) (tea.Model, tea.Cmd) {
	s := m.store[msg.id]
	mod, ok := module.Lookup(msg.id)
	if s == nil || !ok || s.collecting || m.frozen {
		return m, nil
	}
	s.collecting = true
	return m, collectCmd(mod)
}

// refresh forces a collection of the active module now; a pending tick is
// harmless because onTick skips while collecting.
func (m Model) refresh() tea.Cmd {
	e := m.activeEntry()
	if e == nil || e.Module.Interval() == 0 {
		return nil
	}
	s := m.store[e.Module.ID()]
	if s.collecting {
		return nil
	}
	s.collecting = true
	return collectCmd(e.Module)
}

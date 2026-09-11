// Package module defines the interface every collector implements and the
// registry that orders them (HANDOFF §4.1).
package module

import (
	"context"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zebadrabbit/sitrep/internal/detect"
)

// AvailState is the result of Detect.
type AvailState int

const (
	Available AvailState = iota
	Degraded
	NeedsRoot
	Missing
	NeedsAck
	Unsupported
)

func (s AvailState) String() string {
	return [...]string{"available", "degraded", "needs-root", "missing", "needs-ack", "unsupported"}[s]
}

// Availability is Detect's answer plus a human reason.
type Availability struct {
	State  AvailState
	Reason string
}

// Flags describe a module's behavior to the app.
type Flags struct {
	ApplianceSensitive bool // off until acked on an appliance
	Slow               bool // 10s collect timeout instead of 2s
	NeedsRootForFull   bool // some fields show ◐ unprivileged
}

// Data is whatever a module's Collect returns. Views type-assert it.
type Data = any

// Module is the compiled-in unit. See docs/DECISIONS.md for the three
// deltas from HANDOFF §4.1 (no Hotkey, added Update, added Info).
type Module interface {
	ID() string
	Title() string
	Flags() Flags
	Detect(ctx context.Context, env detect.Env) Availability
	Interval() time.Duration
	// Collect runs off the UI goroutine and MUST respect ctx.
	Collect(ctx context.Context) (Data, error)
	// Update receives tab-local key presses when this tab is active.
	Update(msg tea.Msg) tea.Cmd
	// Card is ≤ 6 lines, ≤ 3 primary numbers: the Overview contribution.
	Card(d Data, w int) string
	// View is the full tab. h == 0 means unbounded (one-shot CLI output).
	View(d Data, w, h int) string
	// Keys are tab-local bindings, listed in the footer after │.
	Keys() []key.Binding
	// Info is the `modules info <id>` text: what it collects, needs, execs.
	Info() string
}

// DataMsg carries a finished collection back to the UI.
type DataMsg struct {
	ID   string
	Data Data
	Err  error
	Took time.Duration
}

// Timeout is the per-collection deadline.
func Timeout(m Module) time.Duration {
	if m.Flags().Slow {
		return 10 * time.Second
	}
	return 2 * time.Second
}

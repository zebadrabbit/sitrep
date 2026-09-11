// Package modules registers every compiled-in module in display order.
// Hotkeys 1..9,0 follow this order over enabled modules (HANDOFF §4.1).
package modules

import (
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules/overview"
	"github.com/zebadrabbit/sitrep/internal/modules/ports"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
)

// Register wires the module set. demo routes every collector to fixtures.
func Register(demo bool) {
	module.Register(overview.New())
	module.Register(ports.New(demo))
	module.Register(system.New(demo))
}

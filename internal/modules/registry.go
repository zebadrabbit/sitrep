// Package modules registers every compiled-in module in display order.
// Hotkeys 1..9,0 follow this order over enabled modules (HANDOFF §4.1).
package modules

import (
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules/disks"
	"github.com/zebadrabbit/sitrep/internal/modules/docker"
	"github.com/zebadrabbit/sitrep/internal/modules/network"
	"github.com/zebadrabbit/sitrep/internal/modules/overview"
	"github.com/zebadrabbit/sitrep/internal/modules/ports"
	"github.com/zebadrabbit/sitrep/internal/modules/services"
	"github.com/zebadrabbit/sitrep/internal/modules/system"
	"github.com/zebadrabbit/sitrep/internal/modules/updates"
)

// Register wires the module set. demo routes every collector to fixtures.
// Ports gets the docker module's lookups so docker▸<name> resolves; the
// docker module keeps its last collection for that, nothing global.
func Register(demo bool) {
	p := ports.New(demo)
	d := docker.New(demo)
	p.SetContainerLookup(
		func(proto string, port int) (ports.ContainerRef, bool) {
			r, ok := d.LookupPort(proto, port)
			return ports.ContainerRef{Name: r.Name, Image: r.Image}, ok
		},
		func(id string) (ports.ContainerRef, bool) {
			r, ok := d.LookupID(id)
			return ports.ContainerRef{Name: r.Name, Image: r.Image}, ok
		},
	)
	module.Register(overview.New())
	module.Register(p)
	module.Register(system.New(demo))
	module.Register(services.New(demo))
	module.Register(disks.New(demo))
	module.Register(network.New(demo))
	module.Register(d)
	module.Register(updates.New(demo))
}

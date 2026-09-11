// Package version holds build metadata injected via -ldflags.
package version

import "runtime"

// Set by the Makefile: -X github.com/zebadrabbit/sitrep/internal/version.Version=...
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// GoVersion is the toolchain that built the binary.
func GoVersion() string { return runtime.Version() }

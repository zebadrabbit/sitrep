// Package sitrep exposes the captured fixtures so --demo works with no host
// access and no files on disk. go:embed cannot reach a parent directory, so
// the embed lives at the module root.
package sitrep

import "embed"

// Fixtures holds testdata/fixtures/<module>/<file>.
//
//go:embed testdata/fixtures
var Fixtures embed.FS

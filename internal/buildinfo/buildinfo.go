// Package buildinfo exposes provenance injected by candidate builds.
package buildinfo

import "fmt"

var Version = "development"
var Revision = "unknown"

func String() string { return fmt.Sprintf("%s (%s)", Version, Revision) }

// Package branding contains the shared application artwork.
package branding

import _ "embed"

// Icon is embedded so development binaries do not need an installed icon theme.
//
//go:embed app-icon.svg
var Icon string

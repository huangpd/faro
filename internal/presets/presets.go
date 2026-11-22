// Package presets holds the one text a new install starts with: neutral rules
// (everything through the proxy, what is in China direct). Anything more
// specific is the person's own to write; Faro ships none of it.
package presets

import _ "embed"

//go:embed data/base.rules
var base string

// Base is the text a new install starts with.
func Base() string { return base }

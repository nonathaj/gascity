// Package dolt embeds the dolt database management pack for bundling into the gc binary.
package dolt

import "embed"

// PackFS contains the dolt pack files: pack.toml, doctor/, commands/, and formulas/.
//
//go:embed pack.toml doctor commands formulas
var PackFS embed.FS

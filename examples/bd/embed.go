// Package bd embeds the bd (beads) provider pack for bundling into the gc binary.
package bd

import "embed"

// PackFS contains the bd pack files: pack.toml and doctor/.
//
//go:embed pack.toml doctor
var PackFS embed.FS

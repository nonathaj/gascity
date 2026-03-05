// Package bd embeds the bd (beads) provider pack for bundling into the gc binary.
package bd

import "embed"

// PackFS contains the bd pack files: pack.toml, doctor/, formulas/, and prompts/.
//
//go:embed pack.toml doctor formulas prompts
var PackFS embed.FS

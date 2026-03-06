package main

import "path/filepath"

// materializeBuiltinPrompts writes embedded prompt files to .gc/prompts/.
// Files are always overwritten to stay in sync with the gc binary version.
// Uses materializeFS to walk the embed.FS — no hardcoded filename list.
func materializeBuiltinPrompts(cityPath string) error {
	return materializeFS(defaultPrompts, "prompts",
		filepath.Join(cityPath, ".gc", "prompts"))
}

// materializeBuiltinFormulas writes embedded default formula files to .gc/formulas/.
// Files are always overwritten to stay in sync with the gc binary version.
// Uses materializeFS to walk the embed.FS — no hardcoded filename list.
func materializeBuiltinFormulas(cityPath string) error {
	return materializeFS(defaultFormulas, "formulas",
		filepath.Join(cityPath, ".gc", "formulas"))
}

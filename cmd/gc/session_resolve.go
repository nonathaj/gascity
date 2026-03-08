// session_resolve.go provides CLI-level session resolution.
// The core resolution logic lives in internal/session.ResolveSessionID;
// this file provides a thin CLI wrapper and the looksLikeBeadID helper.
package main

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// resolveSessionID delegates to session.ResolveSessionID.
func resolveSessionID(store beads.Store, identifier string) (string, error) {
	return session.ResolveSessionID(store, identifier)
}

// looksLikeBeadID returns true if the identifier looks like a bead ID
// rather than a template name. Bead IDs use the "gc-" prefix (e.g., "gc-42").
// Note: "gc-" is a reserved prefix — template names starting with "gc-" will
// be treated as bead IDs and bypass name resolution.
func looksLikeBeadID(s string) bool {
	return strings.HasPrefix(s, "gc-")
}

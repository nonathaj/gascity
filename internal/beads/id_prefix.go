package beads

import "strings"

// IDHasPrefix reports whether id was minted under the issue prefix prefix, i.e.
// has the "<prefix>-<suffix>" shape bd gives every bead in a store, wisps
// ("<prefix>-wisp-<suffix>") included. The comparison ignores case, surrounding
// whitespace, and hyphens around prefix. An empty prefix matches no id.
func IDHasPrefix(id, prefix string) bool {
	prefix = normalizeIDPrefix(prefix)
	if prefix == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), prefix+"-")
}

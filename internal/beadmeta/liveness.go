package beadmeta

import "strings"

// Bead status vocabulary. Status is a first-class bead column rather than
// engine-minted metadata, but its spellings and the liveness rule over them
// live here because beadmeta is the leaf package every consumer of that rule
// already imports: internal/config renders it into the routed work_query and
// scale_check shell queries, and cmd/gc applies it at the claim admission
// doors. Declaring it once is what keeps the "scale_check <-> work_query
// protocol-mismatch" hazard class closed — the class where the query that
// decides *whether to spawn* and the query that decides *what to claim*
// disagree, and a session is spawned for work it can never claim.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusClosed     = "closed"
)

// IsLiveStatus reports whether a bead status means the row is still live work.
//
// Liveness is deliberately a deny-list over the one terminal status rather
// than an allow-list of live ones. The question every consumer actually asks
// is "has the store closed this row?", and answering it that way has two
// properties an allow-list lacks:
//
//   - in_progress stays live, so the adoption door can keep serving a bead
//     back to the session that already holds it (reason=existing_assignment).
//   - a status this build has never heard of stays visible instead of being
//     silently starved, so adding a status to bd cannot wedge the fleet.
//
// An empty status is live for the same reason: a missing field is not evidence
// of closure, and treating it as dead would starve every store or fixture that
// omits it.
//
// This is a store-truth test, not a heuristic — there is no "looks stale"
// judgment here, only "the store says this row is closed, therefore it is not
// work."
func IsLiveStatus(status string) bool {
	return !strings.EqualFold(strings.TrimSpace(status), StatusClosed)
}

// LivenessJQSelect renders IsLiveStatus as a jq select(...) expression for the
// shell-side consumers, so the rendered queries and the Go predicate stay
// anchored to one StatusClosed spelling.
//
// The expression is deliberately free of '$' and backslashes: callers embed it
// in sh -c bodies where a '$' would be expanded by the shell before jq ever
// saw it. It normalizes case only; IsLiveStatus additionally trims surrounding
// whitespace as defense in depth, which no status bd emits actually carries,
// so the two agree on every real row.
func LivenessJQSelect() string {
	return `select(((.status // "") | ascii_downcase) != "` + StatusClosed + `")`
}

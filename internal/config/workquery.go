package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/shellquote"
)

// bdReadyPoolDemandShell returns the canonical bd ready predicate for
// unassigned, non-epic pool demand routed to target. gc.routed_to is the
// canonical persisted routing key: the graph.v2 stamper and the legacy stamper
// both stamp it on every routable bead, including the workflow root (ga-eld2x
// retired the short-lived gc.run_target wire field). This predicate is the main
// source of truth for "is there work on this routed queue?" that both the
// worker (via EffectiveWorkQuery Tier 3) and the reconciler (via
// EffectivePoolDemandQuery, count-form) ask; diverging the two re-introduces
// the protocol-mismatch class (see the "scale_check ↔ work_query
// correspondence" note in engdocs/architecture/dispatch.md).
//
// target is passed as a positional argument to the outer sh -c command, not
// interpolated into the nested shell body. That keeps routes containing shell
// metacharacters as data instead of executable syntax.
func bdReadyIncludeEphemeralArg(includeEphemeralReady bool) string {
	if includeEphemeralReady {
		return " --include-ephemeral"
	}
	return ""
}

// jqMeta renders the jq expression that reads a bead-metadata key with an
// empty-string default, e.g. (.metadata["gc.routed_to"] // ""). Shell/jq
// builders use it so embedded key spellings stay anchored to the beadmeta
// vocabulary constants.
func jqMeta(key string) string {
	return `(.metadata["` + key + `"] // "")`
}

func bdReadyPoolDemandShell(limitFlag string, includeEphemeralReady bool) string {
	return `bd ready` + bdReadyIncludeEphemeralArg(includeEphemeralReady) + ` --metadata-field "` + beadmeta.RoutedToMetadataKey + `=$target" --unassigned --exclude-type=epic --json ` + limitFlag
}

// bdReadyPoolDemandMigrationShell is a temporary raw compatibility probe for
// graph.v2 workflow roots created before gc.routed_to root stamping shipped.
// It is scoped to workflow roots so gc.run_target remains an authoring hint
// everywhere else. Callers must pass its output through
// poolDemandMigrationFilterJQ so a stale divergent gc.run_target cannot remain
// visible once a root carries gc.routed_to. This retirement-window fallback
// requires jq in the default worker/reconciler environment; remove it with the
// Go-side legacy candidates after the backfill completion tracked by ga-dhf44.
func bdReadyPoolDemandMigrationShell(limitFlag string, includeEphemeralReady bool) string {
	return `bd ready` + bdReadyIncludeEphemeralArg(includeEphemeralReady) + ` --metadata-field "` + beadmeta.RunTargetMetadataKey + `=$target" --metadata-field "` + beadmeta.KindMetadataKey + `=` + beadmeta.KindWorkflow + `" --unassigned --exclude-type=epic --json --sort oldest ` + limitFlag
}

func poolDemandMigrationFilterJQ(limit int) string {
	filter := `[.[] | select(` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "")]`
	if limit > 0 {
		filter += ` | .[:` + strconv.Itoa(limit) + `]`
	}
	return shellquote.Join([]string{"jq", filter})
}

func bdQueryEphemeralStatusShell(status string) string {
	return `bd query --json ` + shellquote.Quote("ephemeral=true AND status="+status) + ` --limit=0`
}

func bdQueryEphemeralStatusQuietShell(status string) string {
	return bdQueryEphemeralStatusShell(status) + ` 2>/dev/null`
}

func legacyEphemeralReadyFilterJQ(selector string, limit int) string {
	filter := `[.[] | ` + selector +
		` | select(((.issue_type // .type // "") != "epic"))` +
		` | select(([ (.dependencies // [])[]` +
		` | select((.type // .dep_type // "") as $t | ($t == "blocks" or $t == "waits-for" or $t == "conditional-blocks"))` +
		` | select((.status // .depends_on_status // "") != "closed") ] | length) == 0)]` +
		` | sort_by(.created_at // "")`
	if limit > 0 {
		filter += ` | .[:` + strconv.Itoa(limit) + `]`
	}
	return filter
}

// routedEphemeralSelectorJQ is the jq selector for unassigned routed pool demand
// over an ephemeral (bd query) candidate set: canonical gc.routed_to==$target,
// or the migration triplet (empty gc.routed_to + gc.run_target==$target +
// gc.kind=workflow). It is the ephemeral-store analogue of the durable
// bdReadyPoolDemandShell / bdReadyPoolDemandMigrationShell predicates and is
// shared by the reconciler count-form (legacyEphemeralPoolDemandShell) and the
// batched worker work-query so the two cannot drift on the ephemeral tier.
func routedEphemeralSelectorJQ() string {
	return `select((.assignee // "") == "")` +
		` | select((` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == $target) or ((` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "") and (` + jqMeta(beadmeta.RunTargetMetadataKey) + ` == $target) and (` + jqMeta(beadmeta.KindMetadataKey) + ` == "` + beadmeta.KindWorkflow + `")))`
}

func legacyEphemeralPoolDemandShell(limit int, includeEphemeralReady, quiet bool) string {
	if includeEphemeralReady {
		return `printf "[]"`
	}
	filter := legacyEphemeralReadyFilterJQ(routedEphemeralSelectorJQ(), limit)
	query := bdQueryEphemeralStatusShell("open")
	if quiet {
		query = bdQueryEphemeralStatusQuietShell("open")
	}
	jqStderr := ""
	if quiet {
		jqStderr = ` 2>/dev/null`
	}
	return `{ ` + query + ` | jq --arg target "$target" ` + shellquote.Quote(filter) + jqStderr + `; } || printf "[]"`
}

// poolDemandFirstRowFunctionScript emits the work_query Tier 3 function: it
// reads the first ready, unassigned, routed bead for the supplied target,
// prints it, and exits 0. The caller appends a terminal fallthrough
// (printf "[]") for the empty case.
func poolDemandFirstRowFunctionScript(includeEphemeralReady bool) string {
	return `probe_pool_demand() { ` +
		`target="$1"; ` +
		`[ -z "$target" ] && return 1; ` +
		`r=$(` + routedReadyTierCommand(includeEphemeralReady) + `); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		`legacy_candidates=$(` + bdReadyPoolDemandMigrationShell("--limit=20", includeEphemeralReady) + ` 2>/dev/null); ` +
		`r=$(printf "%s" "$legacy_candidates" | ` + poolDemandMigrationFilterJQ(1) + ` 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		`legacy_ephemeral_candidates=$(` + legacyEphemeralPoolDemandShell(20, includeEphemeralReady, true) + `); ` +
		`r=$(printf "%s" "$legacy_ephemeral_candidates" | jq '.[0:1]' 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		`return 1; ` +
		`}; `
}

func routedReadyTierCommand(includeEphemeralReady bool) string {
	// The shared predicate stays order-free so the count-form does no wasted
	// sorting; the worker first-row path asks bd for the oldest candidates.
	// The tier is widened past a single row (limit=20, not limit=1) so a
	// self-blocked head (is_blocked / status==blocked) has Ready routed work
	// behind it to fall through to instead of idle-exiting; the hook layer
	// (filterUnreadyHookCandidates) strips the blocked head from the result.
	return bdReadyPoolDemandShell("--sort oldest --limit=20", includeEphemeralReady) + ` 2>/dev/null`
}

// poolDemandCountShell emits the reconciler count-form for target: it counts
// ready, unassigned, routed demand and prints the array length. It shares the
// canonical and migration predicates with poolDemandFirstRowFunctionScript so
// the reconciler's spawn decision and the worker's claim decision read the
// same demand shape.
//
// Unlike the work_query probe, this form must NOT redirect bd stderr or default
// to zero: a failed `bd ready` has to surface as an error rather than
// masquerade as "no demand", which would silently stop the pool from spawning.
// The && chain ensures any non-zero bd exit short-circuits the whole expression
// (TestEffectiveScaleCheckUsesReadyOnly).
func poolDemandCountShell(target string, includeEphemeralReady bool) string {
	script := `target="$1"; ` +
		`ready_json=$(` + bdReadyPoolDemandShell("--limit 0", includeEphemeralReady) + `) || exit $?; ` +
		`legacy_candidates=$(` + bdReadyPoolDemandMigrationShell("--limit 0", includeEphemeralReady) + `) || exit $?; ` +
		`legacy_json=$(printf "%s" "$legacy_candidates" | ` + poolDemandMigrationFilterJQ(0) + `) || exit $?; ` +
		`legacy_ephemeral_json=$(` + legacyEphemeralPoolDemandShell(0, includeEphemeralReady, false) + `); ` +
		`printf "%s\n%s\n%s\n" "$ready_json" "$legacy_json" "$legacy_ephemeral_json" | jq -s "(add // []) | unique_by(.id) | length"`
	return shellquote.Join([]string{"sh", "-c", script, "--", target})
}

func (a *Agent) poolDemandTarget() string {
	target := a.QualifiedName()
	if a.PoolName != "" {
		target = a.PoolName
	}
	return target
}

func standardAssignedInProgressWorkQueryScript(includeEphemeralReady bool) string {
	return `for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do ` +
		`[ -z "$id" ] && continue; ` +
		`r=$(bd list --status in_progress --assignee="$id" --json --limit=1 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		ephemeralAssignedInProgressProbeScript("id", includeEphemeralReady) +
		`done; `
}

func standardAssignedReadyWorkQueryScript(includeEphemeralReady bool) string {
	return `for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do ` +
		`[ -z "$id" ] && continue; ` +
		`r=$(bd ready` + bdReadyIncludeEphemeralArg(includeEphemeralReady) + ` --assignee="$id" --json --limit=1 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		ephemeralAssignedReadyProbeScript("id", includeEphemeralReady) +
		`done; `
}

func legacyControlAssignedInProgressWorkQueryScript(includeEphemeralReady bool) string {
	return `for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do ` +
		`[ -z "$id" ] && continue; ` +
		`legacy=""; case "$id" in *control-dispatcher) legacy="${id%control-dispatcher}workflow-control";; esac; ` +
		`for cand in "$id" "$legacy"; do ` +
		`[ -z "$cand" ] && continue; ` +
		`r=$(bd list --status in_progress --assignee="$cand" --json --limit=1 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		ephemeralAssignedInProgressProbeScript("cand", includeEphemeralReady) +
		`done; ` +
		`done; `
}

func legacyControlAssignedReadyWorkQueryScript(includeEphemeralReady bool) string {
	return `for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do ` +
		`[ -z "$id" ] && continue; ` +
		`legacy=""; case "$id" in *control-dispatcher) legacy="${id%control-dispatcher}workflow-control";; esac; ` +
		`for cand in "$id" "$legacy"; do ` +
		`[ -z "$cand" ] && continue; ` +
		`r=$(bd ready` + bdReadyIncludeEphemeralArg(includeEphemeralReady) + ` --assignee="$cand" --json --limit=1 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		ephemeralAssignedReadyProbeScript("cand", includeEphemeralReady) +
		`done; ` +
		`done; `
}

func ephemeralAssignedInProgressProbeScript(shellVar string, includeEphemeralReady bool) string {
	_ = includeEphemeralReady
	return `r=$(` + bdQueryEphemeralStatusQuietShell("in_progress") + ` | ` +
		`jq --arg id "$` + shellVar + `" '[.[] | select((.assignee // "") == $id)] | .[:1]' 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; `
}

func ephemeralAssignedReadyProbeScript(shellVar string, includeEphemeralReady bool) string {
	if includeEphemeralReady {
		return ""
	}
	filter := legacyEphemeralReadyFilterJQ(`select((.assignee // "") == $id)`, 1)
	return `r=$(` + bdQueryEphemeralStatusQuietShell("open") + ` | ` +
		`jq --arg id "$` + shellVar + `" ` + shellquote.Quote(filter) + ` 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; `
}

func poolDemandOriginGateScript() string {
	return `case "$GC_SESSION_ORIGIN" in ` +
		`ephemeral|"") ;; ` +
		`*) exit 0 ;; ` +
		`esac; `
}

func routedPoolWorkQueryProbeScript(includeEphemeralReady bool, targetCount int) string {
	script := poolDemandOriginGateScript() + poolDemandFirstRowFunctionScript(includeEphemeralReady)
	for i := 1; i <= targetCount; i++ {
		script += fmt.Sprintf(`probe_pool_demand "$%d"; `, i)
	}
	return script + `printf "[]"`
}

func routedPoolWorkQueryCommand(includeEphemeralReady bool, targets ...string) string {
	args := []string{"sh", "-c", routedPoolWorkQueryProbeScript(includeEphemeralReady, len(targets)), "--"}
	args = append(args, targets...)
	return shellquote.Join(args)
}

// --- Batched work-query (gw-j1m) --------------------------------------------
//
// The per-tier/per-identity/per-route `bd` calls above each cold-open the
// embedded doltlite store (~6-15s on Windows), so the compound work-query fired
// ~6-10 serial cold-opens per idle cycle and blew its deadline. The batched
// form below fetches each broad candidate set ONCE into a shell variable and
// then partitions the tiers with jq (jq processes cached JSON text and does not
// touch the store, so its cost is negligible). This preserves every selection
// predicate and the tier/identity ordering while cutting cold-opens to at most:
//
//	bd list --status in_progress   (durable crash-recovery)
//	bd query ephemeral in_progress (ephemeral crash-recovery)
//	bd ready [--include-ephemeral] (durable ready: assigned + routed tiers)
//	bd query ephemeral open        (1.0.4 ephemeral ready: assigned + routed)
//
// i.e. 2 cold-opens when crash-recovery work is found (the ready fetch is lazy,
// after the in-progress tier), and 3-4 for a fully idle cycle (4 under bd 1.0.4,
// 3 under bd 1.0.5 where ephemeral-ready folds into `bd ready`).

// jqNotEpic is the jq equivalent of bd's `--exclude-type=epic` flag, matching
// the epic test already used by legacyEphemeralReadyFilterJQ.
const jqNotEpic = `(.issue_type // .type // "") != "epic"`

// assignedIdentityMatchJQ selects rows assigned to the jq $id argument and keeps
// only the first, reproducing the `--assignee=<id> ... --limit=1` /
// `.[:1]` shape of the per-identity probes it replaces. It runs over an
// already-fetched array cached in a shell variable, so the identity fan-out
// costs one jq process per candidate instead of one bd cold-open per candidate.
const assignedIdentityMatchJQ = `[.[] | select((.assignee // "") == $id)] | .[:1]`

// batchedCandidateProbe emits one "filter the cached JSON array in $jsonVar by
// the candidate in $candVar, print it and exit 0 if non-empty" step. jqFilter
// reads the jq $id argument.
func batchedCandidateProbe(jsonVar, candVar, jqFilter string) string {
	return `r=$(printf '%s' "$` + jsonVar + `" | jq -c --arg id "$` + candVar + `" ` + shellquote.Quote(jqFilter) + ` 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; `
}

// batchedInProgressFetch performs the single cold-open pair that feeds every
// assigned in-progress (crash-recovery) candidate probe: one `bd list --status
// in_progress` for durable work and one ephemeral `bd query` (durable and
// ephemeral live in separate stores under bd 1.0.4, and `bd list
// --include-ephemeral` is not 1.0.4-compatible, so the two stay distinct). Each
// result is cached and defaulted to "[]" so a transient store error degrades the
// tier to "no work" exactly as the old per-call `2>/dev/null` did.
func batchedInProgressFetch() string {
	return `all_inprog=$(bd list --status in_progress --json --limit 0 2>/dev/null); [ -n "$all_inprog" ] || all_inprog='[]'; ` +
		`eph_inprog=$(` + bdQueryEphemeralStatusQuietShell("in_progress") + `); [ -n "$eph_inprog" ] || eph_inprog='[]'; `
}

// batchedReadyFetch performs the single `bd ready` cold-open feeding both the
// assigned-ready and routed pool-demand tiers (partitioned in jq), plus the
// 1.0.4 ephemeral-ready `bd query`. Under bd 1.0.5 (`--include-ephemeral`) the
// ephemeral-ready rows are already in `bd ready`, so no separate ephemeral fetch
// is issued — matching the pre-batch behavior where the ephemeral-ready probes
// were compiled out.
func batchedReadyFetch(includeEphemeralReady bool) string {
	s := `all_ready=$(bd ready` + bdReadyIncludeEphemeralArg(includeEphemeralReady) + ` --json --limit 0 2>/dev/null); [ -n "$all_ready" ] || all_ready='[]'; `
	if !includeEphemeralReady {
		s += `eph_open=$(` + bdQueryEphemeralStatusQuietShell("open") + `); [ -n "$eph_open" ] || eph_open='[]'; `
	}
	return s
}

// batchedAssignedInProgressBody probes the cached durable then ephemeral
// in-progress sets for the current candidate (crash recovery, Tier 1).
func batchedAssignedInProgressBody(candVar string) string {
	return batchedCandidateProbe("all_inprog", candVar, assignedIdentityMatchJQ) +
		batchedCandidateProbe("eph_inprog", candVar, assignedIdentityMatchJQ)
}

// batchedAssignedReadyBody probes the cached durable ready set (and, under bd
// 1.0.4, the ephemeral-open set with the full ready filter) for the current
// candidate (pre-assigned ready, Tier 2). Epics are intentionally NOT excluded
// here: an agent must resume its own assigned epic wisp (the patrol-loop
// pattern) — same asymmetry as the pre-batch assigned tier.
func batchedAssignedReadyBody(candVar string, includeEphemeralReady bool) string {
	body := batchedCandidateProbe("all_ready", candVar, assignedIdentityMatchJQ)
	if !includeEphemeralReady {
		body += batchedCandidateProbe("eph_open", candVar, legacyEphemeralReadyFilterJQ(`select((.assignee // "") == $id)`, 1))
	}
	return body
}

// standardIdentityLoop and legacyControlIdentityLoop reproduce the exact
// identity fan-out of the pre-batch assigned tiers: $GC_SESSION_ID >
// $GC_SESSION_NAME > $GC_ALIAS, and (control-dispatcher only) each identity's
// legacy workflow-control alias. body references $id (standard) or $cand
// (legacy control) as the candidate identity.
func standardIdentityLoop(body string) string {
	return `for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do ` +
		`[ -z "$id" ] && continue; ` +
		body +
		`done; `
}

func legacyControlIdentityLoop(body string) string {
	return `for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do ` +
		`[ -z "$id" ] && continue; ` +
		`legacy=""; case "$id" in *control-dispatcher) legacy="${id%control-dispatcher}workflow-control";; esac; ` +
		`for cand in "$id" "$legacy"; do ` +
		`[ -z "$cand" ] && continue; ` +
		body +
		`done; ` +
		`done; `
}

// batchedRoutedCanonicalSelectJQ mirrors bdReadyPoolDemandShell
// (`--metadata-field gc.routed_to=$target --unassigned --exclude-type=epic
// --sort oldest --limit=20`) as a jq partition over the cached `bd ready` array.
// bd already computed readiness for that array, so this reproduces only the
// flag-level post-filters and deliberately does NOT re-derive dependency
// readiness (that stays bd's job, exactly as the pre-batch predicate relied on
// `bd ready`). The tier stays wider than one row so a self-blocked head has
// ready routed work behind it for the hook layer (filterUnreadyHookCandidates)
// to fall through to.
func batchedRoutedCanonicalSelectJQ() string {
	return `[.[] | select((.assignee // "") == "") | select(` + jqNotEpic + `) | select(` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == $target)] | sort_by(.created_at // "") | .[:20]`
}

// batchedRoutedMigrationSelectJQ mirrors bdReadyPoolDemandMigrationShell +
// poolDemandMigrationFilterJQ(1): unassigned, non-epic, gc.run_target==$target,
// gc.kind=workflow, and gc.routed_to still empty (a divergent routed_to means
// the canonical key wins and the run_target hint is ignored), oldest first.
func batchedRoutedMigrationSelectJQ() string {
	return `[.[] | select((.assignee // "") == "") | select(` + jqNotEpic + `) | select(` + jqMeta(beadmeta.RunTargetMetadataKey) + ` == $target) | select(` + jqMeta(beadmeta.KindMetadataKey) + ` == "` + beadmeta.KindWorkflow + `") | select(` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "")] | sort_by(.created_at // "") | .[:1]`
}

// batchedPoolDemandFunctionScript defines probe_pool_demand over the cached
// ready sets: canonical gc.routed_to, then the gc.run_target migration
// fallback, then (bd 1.0.4) the ephemeral routed set. Same order and same
// predicates as poolDemandFirstRowFunctionScript, but zero additional
// cold-opens per target.
func batchedPoolDemandFunctionScript(includeEphemeralReady bool) string {
	script := `probe_pool_demand() { ` +
		`target="$1"; ` +
		`[ -z "$target" ] && return 1; ` +
		`r=$(printf '%s' "$all_ready" | jq -c --arg target "$target" ` + shellquote.Quote(batchedRoutedCanonicalSelectJQ()) + ` 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; ` +
		`r=$(printf '%s' "$all_ready" | jq -c --arg target "$target" ` + shellquote.Quote(batchedRoutedMigrationSelectJQ()) + ` 2>/dev/null); ` +
		`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; `
	if !includeEphemeralReady {
		ephFilter := legacyEphemeralReadyFilterJQ(routedEphemeralSelectorJQ(), 1)
		script += `r=$(printf '%s' "$eph_open" | jq -c --arg target "$target" ` + shellquote.Quote(ephFilter) + ` 2>/dev/null); ` +
			`[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0; `
	}
	return script + `return 1; }; `
}

// EffectiveWorkQuery returns the work query command for this agent.
// If WorkQuery is set, returns it as-is. Otherwise returns the default
// three-tier query with multi-identifier assignee resolution.
//
// Assignee resolution order: $GC_SESSION_ID (bead ID) > $GC_SESSION_NAME
// (tmux session name) > $GC_ALIAS (named identity / qualified name).
// All three are checked so work is found regardless of which identifier
// was used when assigning.
//
// State priority: in_progress+assigned (crash recovery) >
// ready+assigned (pre-assigned) > ready+unassigned+routed_to (pool).
// Executable formula roots can be epic-typed; the bead storage policy decides
// whether those roots are history-backed, no-history, or ephemeral for the
// configured bd compatibility mode. Molecule containers are not routable
// demand.
//
// Parent epics are excluded from the routed (pool) tier only
// (--exclude-type=epic). An unassigned parent epic has no executable spec —
// its semantic is "all children done" — so a pool worker claiming one does
// undefined work (gc-udx; the repro is a routed parent epic, see
// TestEffectiveWorkQuerySkipsEpicLeafScenario). The assigned tiers do NOT
// exclude epics: work already assigned to this agent is owned, and the
// patrol-loop pattern (gastown witness/refinery/deacon) can self-assign an
// epic wisp that the agent must resume after a session restart. Excluding
// epics there silently stranded those wisps (gc hook exited 1 with empty
// output). Roles that need different behavior still opt in via an explicit
// work_query in their agent config; that custom query is returned unchanged
// above.
//
// When the reconciler runs the query for demand detection (no session
// context), all identity vars are empty → assignee tiers skip → only
// the routed_to tier fires to detect new demand.
//
// Tier 3's canonical and migration predicates are shared with
// EffectivePoolDemandQuery so reconciler spawn decisions and worker claim
// decisions stay symmetric.
func (a *Agent) EffectiveWorkQuery() string {
	return a.effectiveWorkQuery(false)
}

// EffectiveWorkQueryForBeads returns the default work query using the bd
// compatibility semantics configured for the city.
func (a *Agent) EffectiveWorkQueryForBeads(beads BeadsConfig) string {
	return a.effectiveWorkQuery(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectiveWorkQuery(includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	legacyTarget := legacyWorkflowControlQualifiedName(target)
	if legacyTarget == "" {
		// Fetches are staged so crash-recovery short-circuits before the ready
		// fetch: in-progress tier first, then (only if it finds nothing) the
		// ready fetch feeding the assigned-ready and routed pool tiers.
		script := batchedInProgressFetch() +
			standardIdentityLoop(batchedAssignedInProgressBody("id")) +
			batchedReadyFetch(includeEphemeralReady) +
			standardIdentityLoop(batchedAssignedReadyBody("id", includeEphemeralReady)) +
			poolDemandOriginGateScript() +
			batchedPoolDemandFunctionScript(includeEphemeralReady) +
			`probe_pool_demand "$1"; ` +
			`printf "[]"`
		return shellquote.Join([]string{"sh", "-c", script, "--", target})
	}
	script := batchedInProgressFetch() +
		legacyControlIdentityLoop(batchedAssignedInProgressBody("cand")) +
		batchedReadyFetch(includeEphemeralReady) +
		legacyControlIdentityLoop(batchedAssignedReadyBody("cand", includeEphemeralReady)) +
		poolDemandOriginGateScript() +
		batchedPoolDemandFunctionScript(includeEphemeralReady) +
		`probe_pool_demand "$1"; ` +
		`probe_pool_demand "$2"; ` +
		`printf "[]"`
	return shellquote.Join([]string{"sh", "-c", script, "--", target, legacyTarget})
}

// EffectiveAssignedInProgressQuery returns the assigned-in-progress-only command
// for prompt templates that spell out crash recovery as a separate startup tier.
// A custom WorkQuery is treated as the caller-owned full discovery contract, so
// split-tier prompts may run that same custom command in each query slot.
func (a *Agent) EffectiveAssignedInProgressQuery() string {
	return a.effectiveAssignedInProgressQuery(false)
}

// EffectiveAssignedInProgressQueryForBeads returns the assigned-in-progress
// query using the bd compatibility semantics configured for the city.
func (a *Agent) EffectiveAssignedInProgressQueryForBeads(beads BeadsConfig) string {
	return a.effectiveAssignedInProgressQuery(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectiveAssignedInProgressQuery(includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	if legacyWorkflowControlQualifiedName(target) != "" {
		return shellquote.Join([]string{"sh", "-c", legacyControlAssignedInProgressWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
	}
	return shellquote.Join([]string{"sh", "-c", standardAssignedInProgressWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
}

// EffectiveAssignedReadyQuery returns the assigned-ready-only command for
// prompt templates that spell out claim-first startup in separate tiers. A
// custom WorkQuery is treated as the caller-owned full discovery contract, so
// split-tier prompts may run that same custom command in each query slot.
func (a *Agent) EffectiveAssignedReadyQuery() string {
	return a.effectiveAssignedReadyQuery(false)
}

// EffectiveAssignedReadyQueryForBeads returns the assigned-ready-only query
// using the bd compatibility semantics configured for the city.
func (a *Agent) EffectiveAssignedReadyQueryForBeads(beads BeadsConfig) string {
	return a.effectiveAssignedReadyQuery(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectiveAssignedReadyQuery(includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	if legacyWorkflowControlQualifiedName(target) != "" {
		return shellquote.Join([]string{"sh", "-c", legacyControlAssignedReadyWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
	}
	return shellquote.Join([]string{"sh", "-c", standardAssignedReadyWorkQueryScript(includeEphemeralReady) + `printf "[]"`})
}

// EffectiveRoutedPoolQuery returns the routed-pool-only command for prompt
// templates that spell out claim-first startup in separate tiers. It is the
// prompt-side counterpart to EffectiveWorkQuery's routed pool tier.
func (a *Agent) EffectiveRoutedPoolQuery() string {
	return a.effectiveRoutedPoolQuery(false)
}

// EffectiveRoutedPoolQueryForBeads returns the routed-pool-only command using
// the bd compatibility semantics configured for the city.
func (a *Agent) EffectiveRoutedPoolQueryForBeads(beads BeadsConfig) string {
	return a.effectiveRoutedPoolQuery(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectiveRoutedPoolQuery(includeEphemeralReady bool) string {
	if a.WorkQuery != "" {
		return a.WorkQuery
	}
	target := a.poolDemandTarget()
	legacyTarget := legacyWorkflowControlQualifiedName(target)
	if legacyTarget == "" {
		return routedPoolWorkQueryCommand(includeEphemeralReady, target)
	}
	return routedPoolWorkQueryCommand(includeEphemeralReady, target, legacyTarget)
}

func legacyWorkflowControlQualifiedName(target string) string {
	target = strings.TrimSpace(target)
	if target == ControlDispatcherAgentName {
		return "workflow-control"
	}
	const suffix = "/" + ControlDispatcherAgentName
	if strings.HasSuffix(target, suffix) {
		return strings.TrimSuffix(target, suffix) + "/workflow-control"
	}
	return ""
}

// EffectiveSlingQuery returns the sling query command template for this agent.
// The template uses {} as a placeholder for the bead ID.
// If SlingQuery is set, returns it as-is. Otherwise returns the default:
// "bd update {} --set-metadata gc.routed_to=<template>"
//
// All agents use metadata-based routing. The reconciler and scale_check
// handle session creation; sling just stamps the target template.
func (a *Agent) EffectiveSlingQuery() string {
	if a.SlingQuery != "" {
		return a.SlingQuery
	}
	return a.DefaultSlingQuery()
}

// DefaultSlingQuery returns the built-in metadata-routing sling query for
// this agent. Callers outside config should prefer this helper over rebuilding
// the command string to preserve the bd boundary invariant.
func (a *Agent) DefaultSlingQuery() string {
	return "bd update {} --set-metadata " + beadmeta.RoutedToMetadataKey + "=" + a.QualifiedName()
}

// EffectiveDefaultSlingFormula returns the default sling formula for
// this agent, or "" if none is set.
func (a *Agent) EffectiveDefaultSlingFormula() string {
	if a.DefaultSlingFormula != nil {
		return *a.DefaultSlingFormula
	}
	if a.InheritedDefaultSlingFormula != nil {
		return *a.InheritedDefaultSlingFormula
	}
	return ""
}

// DrainTimeoutDuration returns the drain timeout as a time.Duration.
// Defaults to 5m if empty or unparseable.
func (a *Agent) DrainTimeoutDuration() time.Duration {
	if a.DrainTimeout == "" {
		return 5 * time.Minute
	}
	dur, err := time.ParseDuration(a.DrainTimeout)
	if err != nil {
		return 5 * time.Minute
	}
	return dur
}

// EffectivePoolDemandQuery returns the count-form pool-demand query the
// reconciler runs to detect new unassigned routed work. It is the
// reconciler-side counterpart to EffectiveWorkQuery's Tier 3 (the worker
// claim path): both derive their predicates from the same helpers so
// any future change to the pool-demand shape flows to both paths
// simultaneously.
//
// If ScaleCheck is set (user override), it takes precedence and is
// returned as-is. Otherwise the default count-form is returned.
//
// Assigned in-progress work is resumed from session beads, so it must
// not create additional generic pool demand here.
//
// See engdocs/architecture/dispatch.md "scale_check ↔ work_query
// correspondence" and the protocol-mismatch class regression addressed
// by PR #1516.
func (a *Agent) EffectivePoolDemandQuery() string {
	return a.effectivePoolDemandQuery(false)
}

// EffectivePoolDemandQueryForBeads returns the count-form demand query using
// the bd compatibility semantics configured for the city.
func (a *Agent) EffectivePoolDemandQueryForBeads(beads BeadsConfig) string {
	return a.effectivePoolDemandQuery(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectivePoolDemandQuery(includeEphemeralReady bool) string {
	if a.ScaleCheck != "" {
		return a.ScaleCheck
	}
	target := a.poolDemandTarget()
	return poolDemandCountShell(target, includeEphemeralReady)
}

// EffectiveScaleCheck returns the scale check command for this agent.
// Pass-through to EffectivePoolDemandQuery for back-compat with code and
// configs that name the predicate "scale_check"; new call sites should
// prefer EffectivePoolDemandQuery to make the dependency on the
// work_query predicate explicit.
func (a *Agent) EffectiveScaleCheck() string {
	return a.EffectivePoolDemandQuery()
}

// EffectiveMaxActiveSessions returns the agent's max active sessions.
// Priority: agent.MaxActiveSessions > pool.Max > nil (unlimited).
func (a *Agent) EffectiveMaxActiveSessions() *int {
	return a.MaxActiveSessions // nil = unlimited (default)
}

// EffectiveMinActiveSessions returns the agent's min active sessions.
func (a *Agent) EffectiveMinActiveSessions() int {
	if a.MinActiveSessions != nil && *a.MinActiveSessions > 0 {
		return *a.MinActiveSessions
	}
	return 0
}

// SupportsGenericEphemeralSessions reports whether the template may satisfy
// generic controller demand with ephemeral sessions.
func (a *Agent) SupportsGenericEphemeralSessions() bool {
	if a == nil {
		return false
	}
	if m := a.EffectiveMaxActiveSessions(); m != nil && *m == 0 {
		return false
	}
	return true
}

// SupportsMultipleSessions reports whether the template may materialize more
// than one distinct concrete session identity. Unlike
// SupportsGenericEphemeralSessions, max_active_sessions = 0 still represents a
// multi-session template shape even though generic ephemeral session creation
// is disabled.
func (a *Agent) SupportsMultipleSessions() bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.Namepool) != "" || len(a.NamepoolNames) > 0 {
		return true
	}
	maxSessions := a.EffectiveMaxActiveSessions()
	return maxSessions == nil || *maxSessions != 1
}

// UsesCanonicalSingletonPoolIdentity reports whether singleton pool-shaped
// surfaces should use the configured agent identity instead of synthesizing a
// slot identity such as "{name}-1".
func (a *Agent) UsesCanonicalSingletonPoolIdentity() bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.Namepool) != "" || len(a.NamepoolNames) > 0 {
		return false
	}
	maxSessions := a.EffectiveMaxActiveSessions()
	return maxSessions != nil && *maxSessions == 1
}

// SupportsExpandedSessionIdentities reports whether callers should expose or
// discover concrete member identities instead of only the configured identity.
func (a *Agent) SupportsExpandedSessionIdentities() bool {
	if a == nil {
		return false
	}
	if m := a.EffectiveMaxActiveSessions(); m != nil && *m == 0 {
		return false
	}
	return a.SupportsInstanceExpansion() && !a.UsesCanonicalSingletonPoolIdentity()
}

// SupportsInstanceExpansion reports whether the template may have multiple
// simultaneously addressable concrete instances and therefore needs instance
// discovery / synthetic member naming.
//
// max_active_sessions=1 has two distinct flavors:
//
//   - Pool agents (MinActiveSessions or ScaleCheck set) keep pool controller
//     semantics. Non-namepool singleton pools still use the canonical
//     configured identity; see UsesCanonicalSingletonPoolIdentity.
//   - Named-session agents (MaxActiveSessions=1 with a [[named_session]]
//     entry, no Min/ScaleCheck) addressed as just "{name}" — they have a
//     stable canonical identity and a phantom "-1" suffix breaks tools that
//     resolve by qualified name.
//
// We keep instance expansion on for the pool flavor so controller paths still
// run pool reconciliation, and turn it off for the named-session flavor so the
// bare name resolves correctly.
func (a *Agent) SupportsInstanceExpansion() bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.Namepool) != "" || len(a.NamepoolNames) > 0 {
		return true
	}
	m := a.EffectiveMaxActiveSessions()
	if m == nil {
		return true
	}
	if *m < 0 || *m > 1 {
		return true
	}
	// *m == 1: distinguish pool agents (keep numbered instances) from
	// named-session agents (collapse to base identity). Pool agents are
	// identified by an explicit MinActiveSessions or a ScaleCheck override.
	if a.MinActiveSessions != nil || strings.TrimSpace(a.ScaleCheck) != "" {
		return true
	}
	return false
}

// HasUnlimitedSessionCapacity reports whether max_active_sessions is unbounded.
func (a *Agent) HasUnlimitedSessionCapacity() bool {
	if a == nil {
		return false
	}
	m := a.EffectiveMaxActiveSessions()
	return m == nil || *m < 0
}

// ResolvedMaxActiveSessions returns the effective max for this agent,
// inheriting from rig then workspace if not set on the agent directly.
func (a *Agent) ResolvedMaxActiveSessions(cfg *City) *int {
	if m := a.EffectiveMaxActiveSessions(); m != nil {
		return m
	}
	// Inherit from rig.
	if a.Dir != "" && cfg != nil {
		for _, rig := range cfg.Rigs {
			if rig.Name == a.Dir && rig.MaxActiveSessions != nil {
				return rig.MaxActiveSessions
			}
		}
	}
	// Inherit from workspace.
	if cfg != nil && cfg.Workspace.MaxActiveSessions != nil {
		return cfg.Workspace.MaxActiveSessions
	}
	return nil // unlimited
}

// EffectiveOnDeath returns the on_death command for this agent.
// If OnDeath is set, returns it. Otherwise returns the default recovery hook
// that unclaims in-progress work assigned to this concrete agent identity.
func (a *Agent) EffectiveOnDeath() string {
	return a.effectiveOnDeath(false)
}

// EffectiveOnDeathForBeads returns the default on_death command using the bd
// compatibility semantics configured for the city.
func (a *Agent) EffectiveOnDeathForBeads(beads BeadsConfig) string {
	return a.effectiveOnDeath(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectiveOnDeath(includeEphemeralInProgress bool) string {
	if a.OnDeath != "" {
		return a.OnDeath
	}
	route := a.QualifiedName()
	if a.PoolName != "" {
		route = a.PoolName
	}
	_ = includeEphemeralInProgress
	ephemeralRead := bdQueryEphemeralStatusQuietShell("in_progress") + ` | ` +
		`jq -r --arg assignee ` + shellquote.Quote(a.QualifiedName()) + ` '.[] | select((.assignee // "") == $assignee) | [.id, ` + jqMeta(beadmeta.RunTargetMetadataKey) + `, ` + jqMeta(beadmeta.RoutedToMetadataKey) + `] | @tsv' 2>/dev/null; `
	// Reset both assignee and status: clearing assignee alone leaves the bead
	// invisible to every work_query tier (Tier 1 needs assignee match, Tiers
	// 2/3 only match "ready" status). The next worker re-claims via Tier 3.
	// If routed metadata is missing entirely, backfill the canonical
	// gc.run_target route so reopened direct-assigned work does not stay
	// invisible.
	return `{ ` +
		`bd list --assignee=` + a.QualifiedName() +
		` --status=in_progress --json 2>/dev/null | ` +
		`jq -r '.[] | [.id, ` + jqMeta(beadmeta.RunTargetMetadataKey) + `, ` + jqMeta(beadmeta.RoutedToMetadataKey) + `] | @tsv' 2>/dev/null; ` +
		ephemeralRead +
		`} | ` +
		`while IFS="$(printf '\t')" read -r id run_target routed_to; do ` +
		`[ -z "$id" ] && continue; ` +
		`if [ -n "$run_target" ] || [ -n "$routed_to" ]; then ` +
		`bd update "$id" --assignee "" --status open 2>/dev/null; ` +
		`else bd update "$id" --assignee "" --status open --set-metadata ` + shellquote.Quote(beadmeta.RunTargetMetadataKey+"="+route) + ` 2>/dev/null; ` +
		`fi; ` +
		`done`
}

// EffectiveOnBoot returns the on_boot command for this agent.
// If OnBoot is set, returns it. Otherwise returns the default recovery hook
// that unclaims in-progress work routed to this backing config.
func (a *Agent) EffectiveOnBoot() string {
	return a.effectiveOnBoot(false)
}

// EffectiveOnBootForBeads returns the default on_boot command using the bd
// compatibility semantics configured for the city.
func (a *Agent) EffectiveOnBootForBeads(beads BeadsConfig) string {
	return a.effectiveOnBoot(beads.UsesBD105ReadySemantics())
}

func (a *Agent) effectiveOnBoot(includeEphemeralInProgress bool) string {
	if a.OnBoot != "" {
		return a.OnBoot
	}
	template := a.QualifiedName()
	if a.PoolName != "" {
		template = a.PoolName
	}
	_ = includeEphemeralInProgress
	ephemeralRead := bdQueryEphemeralStatusQuietShell("in_progress") + ` | ` +
		`jq -r --arg template "$template" '.[] | select((.assignee // "") == "") | select((` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == $template) or ((` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "") and (` + jqMeta(beadmeta.RunTargetMetadataKey) + ` == $template) and (` + jqMeta(beadmeta.KindMetadataKey) + ` == "` + beadmeta.KindWorkflow + `"))) | .id' 2>/dev/null; `
	return `template=` + shellquote.Quote(template) + `; ` +
		`{ ` +
		`bd list --metadata-field "` + beadmeta.RoutedToMetadataKey + `=$template" --status=in_progress --no-assignee --json 2>/dev/null | ` +
		`jq -r '.[].id' 2>/dev/null; ` +
		`bd list --metadata-field "` + beadmeta.RunTargetMetadataKey + `=$template" --metadata-field "` + beadmeta.KindMetadataKey + `=` + beadmeta.KindWorkflow + `" --status=in_progress --no-assignee --json 2>/dev/null | ` +
		`jq -r '.[] | select(` + jqMeta(beadmeta.RoutedToMetadataKey) + ` == "") | .id' 2>/dev/null; ` +
		ephemeralRead +
		`} | awk 'NF && !seen[$0]++' | ` +
		`xargs -rI{} bd update {} --status open 2>/dev/null`
}

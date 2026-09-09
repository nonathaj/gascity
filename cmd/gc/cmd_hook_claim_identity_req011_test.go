package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// REQ-011 — CHARACTERIZATION TEST. PINS KNOWN-BROKEN BEHAVIOR.
//
// !!! DO NOT "FIX" THIS TEST INTO GREEN. !!!
//
// Everything asserted below is the behavior Gas City exhibits TODAY, and today's
// behavior is WRONG. The defect is tracked by bead gcty-suj (P1). This file
// exists so that the broken contract is written down and cannot drift silently
// while other work (notably W2, which touches assignee verification) moves
// through the same code. When gcty-suj is actually fixed, the assertions here
// are expected to fail — that failure is the signal that the fix landed, and the
// correct response is to REWRITE this file to pin the corrected behavior, never
// to weaken an assertion so it passes again.
//
// # The defect
//
// One claim identity is spelled two ways. `gc hook --claim` ends up stamping the
// bead's `assignee` with the session **id** (e.g. "fe-8szqif"), while the
// role-worker prompt's claim-verification predicate — and bd's own actor check —
// expect the session **name** (e.g. "gascity--gc__implementation-worker-6-pool").
// The two are never equal, so a worker rejects its own valid claim and spins in
// its retry loop until the lease expires.
//
// # Observed evidence
//
// Six consecutive claims, six identical shapes: `assignee` equals `gc.session_id`
// on every one, and `assignee` equals `gc.session_name` on none — while
// `gc.session_name` is present and correct on all six. The table in
// req011ObservedClaims is that raw evidence.
//
// # Correction to the plan's root-cause guess (review finding F8)
//
// The plan called this "likely a one-line resolution bug — make sessionName
// resolve in the pooled path". That is AN OBSERVATION, NOT AN ESTABLISHED ROOT
// CAUSE, and it is inconsistent with the beads themselves. The same resolved
// value gates the `gc.session_name` stamp (cmd/gc/cmd_hook.go:389,401,449;
// cmd/gc/cmd_hook_claim.go:549; mergeRuntimeEnv at cmd/gc/bd_env.go:1734
// propagates empty overrides). If the session name were empty, `gc.session_name`
// could not have been stamped at all — yet every affected bead carries it. So the
// name plainly resolves; "make it resolve" cannot be the fix.
//
// At least two candidate mechanisms remain undistinguished, and gcty-suj must
// distinguish them before changing code:
//
//	(a) The id and the name are written by two different hook runs. The identity
//	    stamp is compare-and-skip and re-runs on adoption, so a later run can
//	    observe a different resolved identity than the one that first claimed.
//	(b) The assignee is not produced by firstNonEmptyHookValue on this path at
//	    all. store.Claim runs through hookClaimBdStore(dir, env, assignee), which
//	    only exports the value as BEADS_ACTOR (see hookClaimEnvMap); bd's own
//	    actor handling is a plausible writer of the field.
//
// TestREQ011IntendedAssigneeFromHookPrecedenceIsTheSessionName below is direct
// evidence FOR candidate (b): the in-repo precedence yields the name, yet the
// name is not what lands on the bead.
//
// # Blast radius: three call sites, not one
//
// The mismatch breaks every place the stamped assignee is compared against the
// worker's own identity, and the worker's retry loop is only the most visible
// one. All three were observed on the claim that produced this file:
//
//	claim verification  the worker rejects its own valid claim and spins
//	bd close            refused, the step cannot be completed
//	bd heartbeat        refused with "issue already claimed by <session id>" --
//	                    bd turning away the row's true owner, so the lease is
//	                    never renewed and expires under a live session
//
// The heartbeat case is the expensive one: the bead is reaped as orphaned while
// the session is still working it. Exporting BEADS_ACTOR as the session *id* is
// the known manual workaround for all three.
//
// # Why the existing unit tests do not catch this
//
// The in-repo claim fakes stamp the assignee they are handed. cmd_hook_test.go's
// fake bd echoes `${BEADS_ACTOR:-}` into the assignee field, and poolClaimOps in
// cmd_hook_claim_stamp_test.go returns `Assignee: assignee` from its Claim seam.
// Both therefore reproduce the INTENDED behavior (assignee == session name) and
// stay green, while real bd writes the session id. Any fix for gcty-suj that is
// validated only against those doubles will not have been validated at all.

// req011Claim is one observed `gc hook --claim` result: what landed in the bead's
// `assignee` field, next to the session identity metadata stamped on the same
// bead by the same claim.
type req011Claim struct {
	bead        string
	assignee    string
	sessionID   string
	sessionName string
}

// req011ObservedClaims is the raw evidence for REQ-011: six consecutive real
// claims, every field read back from the live store on 2026-09-09 (no inferred
// rows — an earlier draft of this table carried an unclaimed logical peer whose
// assignee was empty, and it was dropped). gcty-1xc was added during
// decomposition, gcty-3d5j by W1's implementation worker, and gcty-eo8g by the
// worker that wrote this test — which hit the bug on its own startup claim and
// spun 26 retries without ever working or draining, until stopped by hand.
var req011ObservedClaims = []req011Claim{
	{bead: "gcty-bb8", assignee: "fe-gl5l9y", sessionID: "fe-gl5l9y", sessionName: "gascity--gc__run-operator-2-pool"},
	{bead: "gcty-xa3", assignee: "fe-4021cg", sessionID: "fe-4021cg", sessionName: "gascity--gc__review-synthesizer-1-pool"},
	{bead: "gcty-1xc", assignee: "fe-2y46j1", sessionID: "fe-2y46j1", sessionName: "gascity--gc__task-decomposer-1-pool"},
	{bead: "gcty-b9e", assignee: "fe-lmgp6e", sessionID: "fe-lmgp6e", sessionName: "gascity--gc__requirements-planner-1-pool"},
	{bead: "gcty-3d5j", assignee: "fe-cpgacj", sessionID: "fe-cpgacj", sessionName: "gascity--gc__implementation-worker-1-pool"},
	{bead: "gcty-eo8g", assignee: "fe-8szqif", sessionID: "fe-8szqif", sessionName: "gascity--gc__implementation-worker-6-pool"},
}

// workBead renders the observed claim as the work bead the worker reads back.
func (c req011Claim) workBead() beads.Bead {
	return beads.Bead{
		ID:       c.bead,
		Status:   "in_progress",
		Assignee: c.assignee,
		Metadata: beads.StringMap{
			beadmeta.SessionIDMetadataKey:   c.sessionID,
			beadmeta.SessionNameMetadataKey: c.sessionName,
		},
	}
}

// sessionBead renders the claiming session's own bead, whose ID is the session id
// and whose raw "session_name" metadata carries the display name.
func (c req011Claim) sessionBead() beads.Bead {
	return beads.Bead{
		ID:       c.sessionID,
		Metadata: beads.StringMap{"session_name": c.sessionName},
	}
}

// req011WorkerExpectedAssignee is the role-worker startup prompt's claim-
// verification predicate, transcribed from the graph.v2 role-worker template:
//
//	EXPECTED_ASSIGNEE="${BEADS_ACTOR:-${GC_SESSION_NAME:-${GC_SESSION_ID:-${GC_AGENT:-}}}}"
//
// The shell `:-` chain is first-non-empty, which is exactly firstNonEmptyHookValue,
// so the production helper is reused rather than re-implemented. In a pooled
// worker BEADS_ACTOR and GC_SESSION_NAME both hold the session NAME, so the
// predicate resolves to the name and GC_SESSION_ID is never reached.
func req011WorkerExpectedAssignee(beadsActor, sessionName, sessionID, agent string) string {
	return firstNonEmptyHookValue(beadsActor, sessionName, sessionID, agent)
}

// TestREQ011StampedAssigneeIsTheSessionIDNotTheSessionName pins the raw shape of
// the defect: the claim stamps the session id into `assignee`, never the session
// name, even though the correct name is stamped alongside it in the same claim.
//
// KNOWN-BROKEN — tracked by gcty-suj. See the file header before touching this.
func TestREQ011StampedAssigneeIsTheSessionIDNotTheSessionName(t *testing.T) {
	for _, c := range req011ObservedClaims {
		t.Run(c.bead, func(t *testing.T) {
			bead := c.workBead()
			gotID := bead.Metadata[beadmeta.SessionIDMetadataKey]
			gotName := bead.Metadata[beadmeta.SessionNameMetadataKey]

			if bead.Assignee != gotID {
				t.Fatalf("REQ-011 characterization drifted: assignee = %q, want it to equal gc.session_id %q.\n"+
					"If gcty-suj was fixed, rewrite this file to pin the corrected behavior.", bead.Assignee, gotID)
			}
			if gotName == "" {
				t.Fatalf("gc.session_name is empty on %s; the F8 correction in this file's header "+
					"depends on the name being present and correct on every affected bead", c.bead)
			}
			if bead.Assignee == gotName {
				t.Fatalf("REQ-011 characterization drifted: assignee = %q now equals gc.session_name.\n"+
					"That is the FIXED behavior — rewrite this file to pin it, do not delete the pin.", bead.Assignee)
			}
		})
	}
}

// TestREQ011WorkerPredicateCanNeverMatchTheStampedAssignee pins Failure 1: the
// worker's verification predicate resolves to the session name while the bead
// carries the session id, so a worker rejects a claim that is genuinely its own
// and retries forever.
//
// KNOWN-BROKEN — tracked by gcty-suj. See the file header before touching this.
func TestREQ011WorkerPredicateCanNeverMatchTheStampedAssignee(t *testing.T) {
	for _, c := range req011ObservedClaims {
		t.Run(c.bead, func(t *testing.T) {
			// A pooled worker's env: BEADS_ACTOR and GC_SESSION_NAME are the name.
			expected := req011WorkerExpectedAssignee(c.sessionName, c.sessionName, c.sessionID, c.sessionName)

			if expected != c.sessionName {
				t.Fatalf("predicate resolved to %q, want the session name %q", expected, c.sessionName)
			}
			if expected == c.assignee {
				t.Fatalf("REQ-011 characterization drifted: the worker predicate now matches the stamped "+
					"assignee %q.\nThat is the FIXED behavior — rewrite this file to pin it.", c.assignee)
			}
		})
	}
}

// TestREQ011StampedAssigneeIsStillAValidSessionIdentity pins the fact that makes
// this a spelling defect rather than a misroute: the stamped id IS one of the
// session's legitimate assignment identities. The reconciler, which matches
// against the whole identity SET via sessionBeadAssigneeIdentities, is therefore
// unaffected — only the worker's scalar equality check breaks. A fix for
// gcty-suj must not "correct" routing that was never wrong.
func TestREQ011StampedAssigneeIsStillAValidSessionIdentity(t *testing.T) {
	for _, c := range req011ObservedClaims {
		t.Run(c.bead, func(t *testing.T) {
			identities := sessionBeadAssigneeIdentities(c.sessionBead())

			var sawAssignee, sawName bool
			for _, id := range identities {
				if id == c.assignee {
					sawAssignee = true
				}
				if id == c.sessionName {
					sawName = true
				}
			}
			if !sawAssignee {
				t.Fatalf("stamped assignee %q is not among the session's identities %v; "+
					"REQ-011 would be a genuine misroute, not a spelling mismatch", c.assignee, identities)
			}
			if !sawName {
				t.Fatalf("session name %q is not among the session's identities %v", c.sessionName, identities)
			}
		})
	}
}

// TestREQ011IntendedAssigneeFromHookPrecedenceIsTheSessionName is the direct
// evidence for F8 candidate (b). cmd/gc/cmd_hook.go:449 computes the claim
// assignee as:
//
//	firstNonEmptyHookValue(sessionName, sessionID, alias, agentForQuery, resolvedAgentName)
//
// With a session name present that yields the NAME. The bead nevertheless ends up
// carrying the ID. Therefore this call is not what writes the field — so "make
// sessionName resolve" cannot be the fix, and gcty-suj must look downstream, at
// hookClaimBdStore/BEADS_ACTOR and bd's own actor handling.
func TestREQ011IntendedAssigneeFromHookPrecedenceIsTheSessionName(t *testing.T) {
	for _, c := range req011ObservedClaims {
		t.Run(c.bead, func(t *testing.T) {
			// The exact argument order of cmd_hook.go:449.
			intended := firstNonEmptyHookValue(c.sessionName, c.sessionID, "", "gc.implementation-worker", "gc.implementation-worker")

			if intended != c.sessionName {
				t.Fatalf("hook precedence yielded %q, want the session name %q", intended, c.sessionName)
			}
			if intended == c.assignee {
				t.Fatalf("REQ-011 characterization drifted: the hook's intended assignee now equals the "+
					"stamped assignee %q.\nThat is the FIXED behavior — rewrite this file to pin it.", c.assignee)
			}
		})
	}
}

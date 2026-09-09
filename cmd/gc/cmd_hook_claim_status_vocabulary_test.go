package main

// One status vocabulary across both claim admission doors (review findings R1
// and R5).
//
// Focused proof command:
//
//	go test ./cmd/gc/ -run 'TestHookClaimAdoptionDoorNormalizes|TestHookClaimClosedRow' -v
//
// cmd_hook_claim_liveness_test.go pinned the adoption door against the store.
// It could not catch these two, because both are vocabulary mismatches rather
// than missing reads:
//
//   - R1: work_query candidates are unmarshalled straight from bd JSON and never
//     pass through mapBdStatus, so candidate.Status carries bd's SIX-value
//     vocabulary while the selection gate compares against Gas City's THREE.
//     A `review` or `testing` row is advertised as live demand by the work
//     query, spawns a session, then matches neither selection spelling — the
//     spawn-and-drain shape this bead exists to eliminate, one status away.
//   - R5: a closed row owned by ANOTHER session reports a store-truth refusal
//     AND a lost race, when only the first happened.
//
// Traceability: REQ-001, REQ-002, AC-1. Plan work item W2. Review findings
// R1, R5.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// bdExtendedStatuses are the bd statuses outside Gas City's three-value
// vocabulary that mapBdStatus collapses to "open" on every store read. They are
// exactly the values the selection gate could not previously spell.
//
// `blocked` is deliberately absent: it is stripped earlier by
// isSelfBlockedHookCandidate, so it never reaches this gate.
var bdExtendedStatuses = []string{"review", "testing"}

// TestHookClaimAdoptionDoorNormalizesExtendedBdStatus is the R1 regression.
//
// The projection advertises a bead assigned to THIS session under a raw bd
// status the store maps to "open". The work query counts it as live demand (its
// jq filter is a deny-list on `closed`), so a session is spawned for it. The
// adoption door must therefore be able to serve it; skipping it strands the
// session into session.drain_acked_with_assigned_work.
func TestHookClaimAdoptionDoorNormalizesExtendedBdStatus(t *testing.T) {
	for _, rawStatus := range bdExtendedStatuses {
		t.Run(rawStatus, func(t *testing.T) {
			const beadID = "adopt-extended-1"
			query, err := json.Marshal([]beads.Bead{{
				ID:       beadID,
				Status:   rawStatus,
				Assignee: "worker-1",
				Metadata: map[string]string{"gc.routed_to": "route-1"},
			}})
			if err != nil {
				t.Fatalf("marshaling work query: %v", err)
			}

			ops := hookClaimOps{
				Runner: func(string, string) (string, error) { return string(query), nil },
				Claim: func(context.Context, string, []string, string, string) (beads.Bead, bool, error) {
					t.Fatal("a bead already assigned to this session must not reach the claim CAS")
					return beads.Bead{}, false, nil
				},
				// The canonical read goes through the store, where mapBdStatus has
				// already collapsed the raw status to "open".
				LoadCanonical: canonicalRowLoader(beads.Bead{
					ID: beadID, Status: "open", Assignee: "worker-1",
				}),
				DrainAck: func(io.Writer) error { return nil },
			}

			var stdout, stderr bytes.Buffer
			doHookClaim("query", "/tmp/work", hookClaimOptions{
				Assignee:           "worker-1",
				IdentityCandidates: []string{"worker-1"},
				RouteTargets:       []string{"route-1"},
				JSON:               true,
			}, ops, &stdout, &stderr)

			result := decodeHookClaimResultLine(t, stdout.String())
			if result.Action != "work" {
				t.Fatalf("raw bd status %q: action = %q (reason %q), want \"work\": the work query "+
					"counts this row as live demand and spawns a session for it, so the adoption "+
					"door must serve it rather than drain",
					rawStatus, result.Action, result.Reason)
			}
			if result.BeadID != beadID {
				t.Errorf("raw bd status %q: bead_id = %q, want %q", rawStatus, result.BeadID, beadID)
			}
		})
	}
}

// TestHookClaimAdoptionDoorStillRefusesClosedExtendedRow pins the other half of
// R1: normalizing the SELECTION gate must not weaken the store-truth gate behind
// it. A projection advertising a live-looking raw status whose store row is
// closed must still be refused.
func TestHookClaimAdoptionDoorStillRefusesClosedExtendedRow(t *testing.T) {
	for _, rawStatus := range bdExtendedStatuses {
		t.Run(rawStatus, func(t *testing.T) {
			const beadID = "adopt-extended-closed-1"
			query, err := json.Marshal([]beads.Bead{{
				ID:       beadID,
				Status:   rawStatus,
				Assignee: "worker-1",
				Metadata: map[string]string{"gc.routed_to": "route-1"},
			}})
			if err != nil {
				t.Fatalf("marshaling work query: %v", err)
			}

			ops := hookClaimOps{
				Runner: func(string, string) (string, error) { return string(query), nil },
				Claim: func(context.Context, string, []string, string, string) (beads.Bead, bool, error) {
					t.Fatal("a bead already assigned to this session must not reach the claim CAS")
					return beads.Bead{}, false, nil
				},
				LoadCanonical: canonicalRowLoader(beads.Bead{
					ID: beadID, Status: "closed", Assignee: "worker-1",
				}),
				DrainAck: func(io.Writer) error { return nil },
			}

			var stdout, stderr bytes.Buffer
			doHookClaim("query", "/tmp/work", hookClaimOptions{
				Assignee:           "worker-1",
				IdentityCandidates: []string{"worker-1"},
				RouteTargets:       []string{"route-1"},
				JSON:               true,
			}, ops, &stdout, &stderr)

			if result := decodeHookClaimResultLine(t, stdout.String()); result.Action == "work" {
				t.Fatalf("raw bd status %q over a CLOSED store row was served as work (bead %q)",
					rawStatus, result.BeadID)
			}
		})
	}
}

// TestHookClaimClosedRowOwnedByOtherSessionReportsNoLostRace is the R5
// regression.
//
// bead.claim_rejected means live contention (ADR-0009): a claim lost to another
// live claimant. A row the store returns CLOSED is nobody's live work, so the
// store-truth refusal is the whole story — emitting a rejection on top of it
// reports a race that did not happen, into the very telemetry stream used to
// detect this defect class.
func TestHookClaimClosedRowOwnedByOtherSessionReportsNoLostRace(t *testing.T) {
	const beadID = "claim-closed-other-1"
	query, err := json.Marshal([]beads.Bead{{
		ID:       beadID,
		Status:   "open",
		Metadata: map[string]string{"gc.routed_to": "route-1"},
	}})
	if err != nil {
		t.Fatalf("marshaling work query: %v", err)
	}

	var rejected []string
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return string(query), nil },
		Claim: func(context.Context, string, []string, string, string) (beads.Bead, bool, error) {
			// The CAS did not commit, and the row it read back is closed and
			// carries a different assignee.
			return beads.Bead{ID: beadID, Status: "closed", Assignee: "other-session"}, false, nil
		},
		EmitClaimRejected: func(id, existing, attempted string) {
			rejected = append(rejected, id+" existing="+existing+" attempted="+attempted)
		},
		DrainAck: func(io.Writer) error { return nil },
	}

	var stdout, stderr bytes.Buffer
	doHookClaim("query", "/tmp/work", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"route-1"},
		JSON:               true,
	}, ops, &stdout, &stderr)

	if len(rejected) != 0 {
		t.Errorf("bead.claim_rejected emitted for a CLOSED store row: %v\n"+
			"a closed row is not live contention; the store-truth refusal is the whole story",
			rejected)
	}
	// The refusal itself must still be announced — silence on this path is what
	// kept the original claim loop invisible.
	if !strings.Contains(stderr.String(), "store row is not live") {
		t.Errorf("store-truth refusal was not surfaced on stderr; got %q", stderr.String())
	}
}

// TestHookClaimLostRaceToLiveClaimantStillReportsRejection is the non-vacuous
// counterpart to the R5 fix: suppressing the event for CLOSED rows must not
// suppress it for a genuine lost race against a live claimant.
func TestHookClaimLostRaceToLiveClaimantStillReportsRejection(t *testing.T) {
	const beadID = "claim-lost-race-1"
	query, err := json.Marshal([]beads.Bead{{
		ID:       beadID,
		Status:   "open",
		Metadata: map[string]string{"gc.routed_to": "route-1"},
	}})
	if err != nil {
		t.Fatalf("marshaling work query: %v", err)
	}

	var rejected []string
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return string(query), nil },
		Claim: func(context.Context, string, []string, string, string) (beads.Bead, bool, error) {
			// Lost the CAS to a claimant that is genuinely live.
			return beads.Bead{ID: beadID, Status: "in_progress", Assignee: "other-session"}, false, nil
		},
		EmitClaimRejected: func(id, existing, attempted string) {
			rejected = append(rejected, id+" existing="+existing+" attempted="+attempted)
		},
		DrainAck: func(io.Writer) error { return nil },
	}

	var stdout, stderr bytes.Buffer
	doHookClaim("query", "/tmp/work", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"route-1"},
		JSON:               true,
	}, ops, &stdout, &stderr)

	want := beadID + " existing=other-session attempted=worker-1"
	if len(rejected) != 1 || rejected[0] != want {
		t.Errorf("bead.claim_rejected = %v, want exactly [%q]: a lost race to a LIVE "+
			"claimant is real contention and must still be reported", rejected, want)
	}
}

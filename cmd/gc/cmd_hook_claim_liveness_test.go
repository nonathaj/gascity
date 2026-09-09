package main

// Adoption-door half of the store-truth liveness gate (work item W2).
//
// Focused proof command:
//
//	go test ./cmd/gc/ -run 'TestHookClaimRowIsLive|TestHookClaimAdoptionDoor' -v
//
// W1 (cmd_hook_claim_closed_row_test.go) pinned the FRESH-CLAIM door: a row the
// claim CAS returns closed must not be served. It could not pin the ADOPTION
// door, because hookClaimExistingOrAssigned had no store read to intercept — it
// decided from the work_query projection alone. That is the complete and
// sufficient explanation for `reason=existing_assignment` being served for a
// bead closed 13 days earlier: nothing on that path ever asked the store.
//
// These tests pin the adoption door against the same single liveness predicate
// the fresh-claim door uses, and they pin the F4 error semantics settled by this
// bead: fail-closed PER CANDIDATE, always surfaced, never globally stalling.
//
// Traceability: REQ-001, REQ-002, AC-1. Plan work item W2. Review findings F4, F9.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// liveCanonicalRowLoader is the LoadCanonical seam for tests whose work_query
// projection is meant to AGREE with the store: every row read back is live and
// owned by the asking session. It returns no metadata, which also exercises the
// projection-metadata retention the adoption door performs (a canonical read may
// legitimately return a thinner metadata projection than the work query).
func liveCanonicalRowLoader() hookLoadCanonicalFunc {
	return func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, error) {
		return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee}, nil
	}
}

// canonicalRowLoader serves the given rows as authoritative store truth, keyed
// by id, so a test can make the store disagree with the projection.
func canonicalRowLoader(rows ...beads.Bead) hookLoadCanonicalFunc {
	byID := make(map[string]beads.Bead, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	return func(_ context.Context, _ string, _ []string, beadID, _ string) (beads.Bead, error) {
		row, ok := byID[beadID]
		if !ok {
			return beads.Bead{}, errors.New("no canonical row for " + beadID)
		}
		return row, nil
	}
}

// TestHookClaimRowIsLive pins the one shared predicate. Liveness is an exact
// allowlist over the three statuses Gas City normalizes bd's to
// (internal/beads/bdstore.go mapBdStatus), so an unrecognized or empty status is
// NOT live — the predicate is fail-closed by construction, not by a caller
// remembering to handle a default case.
func TestHookClaimRowIsLive(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{status: "open", want: true},
		{status: "in_progress", want: true},
		{status: "IN_PROGRESS", want: true},
		{status: "  open  ", want: true},
		{status: "closed", want: false},
		{status: "CLOSED", want: false},
		{status: "", want: false},
		{status: "archived", want: false},
	} {
		if got := hookClaimRowIsLive(beads.Bead{ID: "row", Status: tc.status}); got != tc.want {
			t.Errorf("hookClaimRowIsLive(status=%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// TestHookClaimAdoptionDoorRefusesClosedRow is the core W2 regression: the
// projection still advertises the bead as this session's in-progress work, but
// the store row is closed. Serving it is the claim loop under repair.
func TestHookClaimAdoptionDoorRefusesClosedRow(t *testing.T) {
	for _, tc := range []struct {
		name             string
		projectionStatus string
	}{
		{name: "existing_assignment_path", projectionStatus: "in_progress"},
		{name: "ready_assignment_path", projectionStatus: "open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const closedID = "adopt-closed-1"
			ops := hookClaimOps{
				Runner: func(string, string) (string, error) {
					return `[{"id":"` + closedID + `","status":"` + tc.projectionStatus +
						`","assignee":"worker-1","metadata":{"gc.routed_to":"route-1"}}]`, nil
				},
				Claim: func(context.Context, string, []string, string, string) (beads.Bead, bool, error) {
					t.Fatal("a bead already assigned to this session must not reach the claim CAS")
					return beads.Bead{}, false, nil
				},
				LoadCanonical: canonicalRowLoader(beads.Bead{
					ID: closedID, Status: "closed", Assignee: "worker-1",
				}),
			}

			var stdout, stderr bytes.Buffer
			doHookClaim("query", "/tmp/work", hookClaimOptions{
				Assignee:           "worker-1",
				IdentityCandidates: []string{"worker-1"},
				RouteTargets:       []string{"route-1"},
				JSON:               true,
			}, ops, &stdout, &stderr)

			result := decodeHookClaimResultLine(t, stdout.String())
			if result.Action == "work" {
				t.Fatalf("closed store row %s adopted as action=work (reason=%q)", closedID, result.Reason)
			}
			if !strings.Contains(stderr.String(), closedID) {
				t.Fatalf("refusal of %s was silent; stderr=%q", closedID, stderr.String())
			}
		})
	}
}

// TestHookClaimAdoptionDoorStillAdoptsLiveRow is the over-fix guard. The gate
// must refuse rows the store says are dead — and nothing else. A session whose
// in-progress work is genuinely live must still be handed it back.
func TestHookClaimAdoptionDoorStillAdoptsLiveRow(t *testing.T) {
	const liveID = "adopt-live-1"
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"` + liveID + `","status":"in_progress","assignee":"worker-1",` +
				`"metadata":{"gc.routed_to":"route-1","gc.root_bead_id":"root-1"}}]`, nil
		},
		LoadCanonical: canonicalRowLoader(beads.Bead{
			ID: liveID, Status: "in_progress", Assignee: "worker-1",
		}),
	}

	var stdout, stderr bytes.Buffer
	doHookClaim("query", "/tmp/work", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"route-1"},
		JSON:               true,
	}, ops, &stdout, &stderr)

	result := decodeHookClaimResultLine(t, stdout.String())
	if result.Action != "work" || result.Reason != "existing_assignment" || result.BeadID != liveID {
		t.Fatalf("live row not adopted: %+v (stderr=%s)", result, stderr.String())
	}
	// The canonical read returned no metadata; the work-query projection's
	// metadata must survive so the result contract stays whole.
	if result.RootBeadID != "root-1" {
		t.Fatalf("root_bead_id = %q, want root-1 (projection metadata must be retained)", result.RootBeadID)
	}
}

// TestHookClaimAdoptionDoorReadErrorFailsClosedPerCandidate pins the F4 decision
// settled by this bead: when the store read that would confirm liveness fails,
// the candidate is refused (never served optimistically) and the refusal is
// surfaced — but the refusal is scoped to that one candidate, so other routed
// work in the same batch is unaffected. Fail-closed globally would invert
// REQ-010 and starve the fleet on a store blip.
func TestHookClaimAdoptionDoorReadErrorFailsClosedPerCandidate(t *testing.T) {
	const (
		unconfirmableID = "adopt-unconfirmable"
		liveID          = "adopt-live-2"
	)
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"` + unconfirmableID + `","status":"in_progress","assignee":"worker-1","metadata":{"gc.routed_to":"route-1"}},` +
				`{"id":"` + liveID + `","status":"in_progress","assignee":"worker-1","metadata":{"gc.routed_to":"route-1"}}]`, nil
		},
		LoadCanonical: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, error) {
			if beadID == unconfirmableID {
				return beads.Bead{}, errors.New("store read timeout")
			}
			return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	doHookClaim("query", "/tmp/work", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"route-1"},
		JSON:               true,
	}, ops, &stdout, &stderr)

	result := decodeHookClaimResultLine(t, stdout.String())
	if result.BeadID == unconfirmableID {
		t.Fatalf("row %s was served although its liveness could not be confirmed", unconfirmableID)
	}
	if result.Action != "work" || result.BeadID != liveID {
		t.Fatalf("result = %+v, want the live candidate %s still served (refusal must be per candidate)", result, liveID)
	}
	if !strings.Contains(stderr.String(), unconfirmableID) {
		t.Fatalf("unconfirmable row %s was skipped silently; stderr=%q", unconfirmableID, stderr.String())
	}
}

// TestHookClaimAdoptionDoorRefusesRowOwnedByAnotherSession covers the third way
// the projection can be stale: the row is live, but the store says it now
// belongs to someone else. Adopting it would put two sessions on one bead.
func TestHookClaimAdoptionDoorRefusesRowOwnedByAnotherSession(t *testing.T) {
	const stolenID = "adopt-stolen"
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"` + stolenID + `","status":"in_progress","assignee":"worker-1","metadata":{"gc.routed_to":"route-1"}}]`, nil
		},
		LoadCanonical: canonicalRowLoader(beads.Bead{
			ID: stolenID, Status: "in_progress", Assignee: "worker-2",
		}),
	}

	var stdout, stderr bytes.Buffer
	doHookClaim("query", "/tmp/work", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"route-1"},
		JSON:               true,
	}, ops, &stdout, &stderr)

	result := decodeHookClaimResultLine(t, stdout.String())
	if result.Action == "work" {
		t.Fatalf("row %s owned by worker-2 was adopted by worker-1: %+v", stolenID, result)
	}
	if !strings.Contains(stderr.String(), stolenID) {
		t.Fatalf("refusal of %s was silent; stderr=%q", stolenID, stderr.String())
	}
}

// TestHookClaimAdoptionDoorReadsStoreOncePerServedCandidate pins the stated
// per-claim latency budget (F4): the gate costs at most ONE canonical read per
// identity-matched candidate, and it stops reading as soon as one verifies. It
// does not read the whole work-query batch.
func TestHookClaimAdoptionDoorReadsStoreOncePerServedCandidate(t *testing.T) {
	reads := map[string]int{}
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"first","status":"in_progress","assignee":"worker-1","metadata":{"gc.routed_to":"route-1"}},` +
				`{"id":"second","status":"in_progress","assignee":"worker-1","metadata":{"gc.routed_to":"route-1"}}]`, nil
		},
		LoadCanonical: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, error) {
			reads[beadID]++
			return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	doHookClaim("query", "/tmp/work", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"route-1"},
		JSON:               true,
	}, ops, &stdout, &stderr)

	if reads["first"] != 1 {
		t.Fatalf("reads[first] = %d, want exactly 1 canonical read for the served candidate", reads["first"])
	}
	if reads["second"] != 0 {
		t.Fatalf("reads[second] = %d, want 0: the door must stop once a candidate verifies", reads["second"])
	}
}

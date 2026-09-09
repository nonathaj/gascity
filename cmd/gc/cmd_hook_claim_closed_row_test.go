package main

// Reproduction for the `gc hook --claim` closed-row loop (work item W1).
//
// Focused proof command:
//
//	go test ./cmd/gc/ -run TestHookClaimClosedRow -v
//
// The defect: a routed worker is served `action=work` for a bead whose
// authoritative store row is already closed. The worker cannot progress it, the
// row re-arms, and the next tick serves the identical entry — a claim loop that
// never drains. `hookCandidateClaimable` gates only on a non-empty id, an empty
// assignee, and a route match; nothing anywhere on the fresh-claim path compares
// the canonical row's status against "still live" before emitting work.
//
// These tests drive only the existing hookClaimOps / hookClaimOptions seams and
// the existing hookClaimCommandRunnerWithEnvContext seam. They introduce no new
// abstraction, so they compile and run against unfixed code and pin the contract
// W2/W3B/W4 must satisfy.
//
// Error semantics (plan-review finding F4) are NOT settled by this bead. W2 owns
// that decision. These tests assert the decomposition's recommendation —
// fail-closed per candidate, surfaced and never silent — so the choice is
// visible and testable rather than implicit. If W2 explicitly overrules it, W2
// updates the sub-cases named below.
//
// Traceability: REQ-008, AC-5 (first half).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// decodeHookClaimResultLine parses the single JSON result line a --json claim
// writes, so assertions read against the documented result contract rather than
// against raw stdout text.
func decodeHookClaimResultLine(t *testing.T, stdout string) hookClaimJSONResult {
	t.Helper()
	line := strings.TrimSpace(stdout)
	if line == "" {
		t.Fatal("claim wrote no JSON result line")
	}
	var result hookClaimJSONResult
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("decoding claim result %q: %v", line, err)
	}
	return result
}

// closedRowWorkQuery renders a work_query projection that still advertises id as
// ready, unassigned, and routed — the stale view that outlives the close.
func closedRowWorkQuery(t *testing.T, id string) string {
	t.Helper()
	output, err := json.Marshal([]beads.Bead{{
		ID:       id,
		Status:   "open",
		Metadata: map[string]string{"gc.routed_to": "route-1"},
	}})
	if err != nil {
		t.Fatalf("marshal work query candidates: %v", err)
	}
	return string(output)
}

// TestHookClaimClosedRowIsNotServedAsWork is the serve test: a row that is
// closed in the authoritative store must never be emitted as action=work, at
// either layer that can admit it.
func TestHookClaimClosedRowIsNotServedAsWork(t *testing.T) {
	const closedID = "closed-row-1"

	// Admission door: the work_query projection is stale (says open), the
	// canonical read-back that the claim performs returns the closed row. The
	// hook must refuse to serve it as work.
	t.Run("admission_door_refuses_closed_canonical_row", func(t *testing.T) {
		workQuery := closedRowWorkQuery(t, closedID)
		ops := hookClaimOps{
			Runner: func(string, string) (string, error) { return workQuery, nil },
			Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
				// Store truth: bd applied the mutation, and the canonical
				// read-back shows the row is closed.
				return beads.Bead{
					ID:       beadID,
					Status:   "closed",
					Assignee: assignee,
					Metadata: map[string]string{"gc.routed_to": "route-1"},
				}, true, nil
			},
			DrainAck: func(io.Writer) error { return nil },
		}

		var stdout, stderr bytes.Buffer
		doHookClaim("query", ".", hookClaimOptions{
			Assignee:           "worker-1",
			IdentityCandidates: []string{"worker-1"},
			RouteTargets:       []string{"route-1"},
			JSON:               true,
		}, ops, &stdout, &stderr)

		result := decodeHookClaimResultLine(t, stdout.String())
		if result.Action == "work" {
			t.Fatalf("closed row %s served as action=work (reason=%q); a closed row is not claimable work", closedID, result.Reason)
		}
	})

	// Store helper: hookClaimWithBdStore already re-reads the canonical bead and
	// already verifies the assignee stuck. A closed canonical row must not come
	// back as a successful claim either.
	t.Run("store_helper_refuses_closed_canonical_row", func(t *testing.T) {
		originalRunner := hookClaimCommandRunnerWithEnvContext
		t.Cleanup(func() { hookClaimCommandRunnerWithEnvContext = originalRunner })

		hookClaimCommandRunnerWithEnvContext = func(_ context.Context, _ map[string]string) beads.CommandRunner {
			return func(_ string, name string, args ...string) ([]byte, error) {
				if name != "bd" {
					t.Fatalf("command name = %q, want bd", name)
				}
				switch {
				case reflect.DeepEqual(args, []string{"update", closedID, "--claim", "--json"}):
					return []byte(`[{"id":"` + closedID + `","status":"in_progress","assignee":"worker-1"}]`), nil
				case reflect.DeepEqual(args, []string{"show", "--json", closedID}):
					return []byte(`[{"id":"` + closedID + `","status":"closed","assignee":"worker-1"}]`), nil
				default:
					t.Fatalf("unexpected bd args: %#v", args)
					return nil, nil
				}
			}
		}

		_, ok, err := hookClaimWithBdStore(context.Background(), "/rig", nil, closedID, "worker-1")
		if err != nil {
			t.Fatalf("hookClaimWithBdStore: %v", err)
		}
		if ok {
			t.Fatalf("closed canonical row %s reported as a successful claim; want ok=false", closedID)
		}
	})
}

// TestHookClaimClosedRowIsAbsentFromClaimableSetOnNextTick is the re-arm test.
// The loop is a per-tick property: the failure is not that one tick served the
// row, it is that every subsequent tick serves it again. The claimable set is
// internal state, so absence is observed through its only external signal — the
// row is still not emitted as work after the row re-arms.
func TestHookClaimClosedRowIsAbsentFromClaimableSetOnNextTick(t *testing.T) {
	const closedID = "closed-row-rearm"
	workQuery := closedRowWorkQuery(t, closedID)

	ops := hookClaimOps{
		Runner: func(string, string) (string, error) { return workQuery, nil },
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{
				ID:       beadID,
				Status:   "closed",
				Assignee: assignee,
				Metadata: map[string]string{"gc.routed_to": "route-1"},
			}, true, nil
		},
		DrainAck: func(io.Writer) error { return nil },
	}

	for tick := 1; tick <= 2; tick++ {
		var stdout, stderr bytes.Buffer
		doHookClaim("query", ".", hookClaimOptions{
			Assignee:           "worker-1",
			IdentityCandidates: []string{"worker-1"},
			RouteTargets:       []string{"route-1"},
			JSON:               true,
		}, ops, &stdout, &stderr)

		result := decodeHookClaimResultLine(t, stdout.String())
		if result.Action == "work" {
			t.Fatalf("tick %d served closed row %s as action=work (reason=%q); the row must stay out of the claimable set across re-arm", tick, closedID, result.Reason)
		}
		if result.BeadID == closedID {
			t.Fatalf("tick %d returned closed row %s as bead_id", tick, closedID)
		}
	}
}

// TestHookClaimClosedRowStoreReadErrorFailsClosed covers the error/timeout path
// of the canonical store read, asserting the decomposition's recommended F4
// semantics: fail closed for the candidate whose liveness cannot be confirmed,
// surface the refusal, and do not stall the other candidates.
func TestHookClaimClosedRowStoreReadErrorFailsClosed(t *testing.T) {
	// A read error must not be laundered into a silent "nothing happened".
	// Swallowing it is what made the original defect invisible for hours.
	t.Run("conflict_readback_error_is_surfaced", func(t *testing.T) {
		const contendedID = "contended-row"
		originalRunner := hookClaimCommandRunnerWithEnvContext
		t.Cleanup(func() { hookClaimCommandRunnerWithEnvContext = originalRunner })

		hookClaimCommandRunnerWithEnvContext = func(_ context.Context, _ map[string]string) beads.CommandRunner {
			return func(_ string, name string, args ...string) ([]byte, error) {
				if name != "bd" {
					t.Fatalf("command name = %q, want bd", name)
				}
				switch {
				case reflect.DeepEqual(args, []string{"update", contendedID, "--claim", "--json"}):
					return []byte("issue already claimed by other-worker"),
						errors.New("bd: issue already claimed by other-worker")
				case reflect.DeepEqual(args, []string{"show", "--json", contendedID}):
					return nil, errors.New("store read timeout")
				default:
					t.Fatalf("unexpected bd args: %#v", args)
					return nil, nil
				}
			}
		}

		_, ok, err := hookClaimWithBdStore(context.Background(), "/rig", nil, contendedID, "worker-1")
		if ok {
			t.Fatalf("unconfirmable row %s reported as a successful claim", contendedID)
		}
		if err == nil {
			t.Fatalf("canonical read error for %s was swallowed; want it surfaced so the candidate is refused loudly", contendedID)
		}
	})

	// Fail-closed must be per candidate. Refusing the one unconfirmable row must
	// not stall the rest of the fleet's routed work in the same batch.
	t.Run("refusal_is_per_candidate_not_global", func(t *testing.T) {
		candidates, err := json.Marshal([]beads.Bead{
			{ID: "unconfirmable-1", Status: "open", Metadata: map[string]string{"gc.routed_to": "route-1"}},
			{ID: "live-2", Status: "open", Metadata: map[string]string{"gc.routed_to": "route-1"}},
		})
		if err != nil {
			t.Fatalf("marshal candidates: %v", err)
		}

		ops := hookClaimOps{
			Runner: func(string, string) (string, error) { return string(candidates), nil },
			Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
				if beadID == "unconfirmable-1" {
					return beads.Bead{}, false, errors.New("store read timeout")
				}
				return beads.Bead{
					ID:       beadID,
					Status:   "in_progress",
					Assignee: assignee,
					Metadata: map[string]string{"gc.routed_to": "route-1"},
				}, true, nil
			},
			DrainAck: func(io.Writer) error { return nil },
		}

		var stdout, stderr bytes.Buffer
		doHookClaim("query", ".", hookClaimOptions{
			Assignee:           "worker-1",
			IdentityCandidates: []string{"worker-1"},
			RouteTargets:       []string{"route-1"},
			JSON:               true,
		}, ops, &stdout, &stderr)

		result := decodeHookClaimResultLine(t, stdout.String())
		if result.Action != "work" || result.BeadID != "live-2" {
			t.Fatalf("result = (action=%q, bead_id=%q), want the live candidate live-2 to still be served", result.Action, result.BeadID)
		}
		if !strings.Contains(stderr.String(), "skipping unconfirmable-1") {
			t.Fatalf("stderr = %q, want the refused candidate surfaced", stderr.String())
		}
	})
}

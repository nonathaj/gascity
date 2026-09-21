package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// Regression coverage for infra-5b6r: `gc hook --claim` handed a workflow
// topology latch (beadmeta.WorkflowTopologyKinds — the root workflow bead, a
// scope latch, a formula spec) back to a role worker as action=work.
//
// A graph.v2 root legitimately carries gc.routed_to pointing at a role pool —
// that is the #2763 writer fix, and doctor_run_target_backfill.go backfills the
// key when it is missing — so the latch matched hookClaimMatchesRoute like any
// routed step. But graphroute.IsWorkflowTopologyKind states the invariant these
// beads carry: routing never lands on them, agents must never claim them. A role
// worker served one can only release it and drain, and the released latch is
// then re-served to the next session in the pool: sixteen role-worker sessions
// burned on a single fed-build run, across two routes and two formulas, while
// the latch also competed with genuinely routed steps for the same claim.
//
// Both claim selection and default pool demand exclude topology latches.
// Surfacing a root as demand while refusing to claim it creates repeated empty
// worker starts. Executable graph children still surface independently; legacy
// workflow roots remain executable. Public ready/store reads retain topology.
//
// Structurally this is gas-kg6 (isHeldHookCandidate) with one word changed: a
// bead handed back as work that cannot be advanced, is never released, and so is
// re-served forever. The status/assignee tiers cannot catch either — a latch is
// validly ready and validly routed to us — so the kind dimension is filtered.

const latchTestIdentity = "gem-infrastructure--gc__run-operator-1-pool"

// latchWorkQueryRow renders one work-query row carrying the given gc.kind and
// gc.routed_to, matching the projection `bd ready --json` emits. The graph.v2
// contract rides along because a real latch carries it — formula/compile.go
// stamps gc.formula_contract beside gc.kind on the root — and because it is half
// the predicate: without it a gc.kind=workflow row is the LEGACY v1 root, which
// is claimable by design (TestDoHookClaimClaimsLegacyRunTargetWorkflowRoot).
func latchWorkQueryRow(id, status, assignee, kind, routedTo string) string {
	metadata := map[string]string{
		beadmeta.KindMetadataKey:     kind,
		beadmeta.RoutedToMetadataKey: routedTo,
	}
	if kind != "" {
		metadata[beadmeta.FormulaContractMetadataKey] = beadmeta.FormulaContractGraphV2
	}
	row := map[string]any{
		"id":         id,
		"status":     status,
		"issue_type": "task",
		"assignee":   assignee,
		"metadata":   metadata,
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func latchTestClaimOptions() hookClaimOptions {
	return hookClaimOptions{
		Assignee:           latchTestIdentity,
		IdentityCandidates: hookClaimIdentityCandidates(latchTestIdentity),
		RouteTargets:       hookClaimRouteTargets(latchTestIdentity),
		JSON:               true,
	}
}

func decodeClaimResult(t *testing.T, stdout *bytes.Buffer) hookClaimJSONResult {
	t.Helper()
	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nraw: %s", err, stdout.String())
	}
	return result
}

// TestHookClaimDoesNotServeRoutedWorkflowLatch is the primary infra-5b6r
// regression: a latch routed to this session's own role must drain, not claim.
// Every kind in beadmeta.WorkflowTopologyKinds is covered — the field reports
// named gc.kind=workflow, but the invariant is set-wide and a scope latch has
// the identical failure mode.
func TestHookClaimDoesNotServeRoutedWorkflowLatch(t *testing.T) {
	for _, kind := range beadmeta.WorkflowTopologyKinds {
		t.Run(kind, func(t *testing.T) {
			const latchID = "infra-0co"
			runner := func(string, string) (string, error) {
				return `[` + latchWorkQueryRow(latchID, "open", "", kind, latchTestIdentity) + `]`, nil
			}
			ops := hookClaimOps{
				Runner: runner,
				Claim: func(_ context.Context, _ string, _ []string, id, _ string) (beads.Bead, bool, error) {
					t.Fatalf("store.Claim called for workflow-topology bead %q (gc.kind=%s); a latch must never reach the claim mutation", id, kind)
					return beads.Bead{}, false, nil
				},
			}
			var stdout, stderr bytes.Buffer
			doHookClaim("bd ready --json", "/tmp/work", latchTestClaimOptions(), ops, &stdout, &stderr)

			result := decodeClaimResult(t, &stdout)
			if result.Action == "work" {
				t.Fatalf("REGRESSION infra-5b6r: hook served workflow-topology bead %q (gc.kind=%s) as action=work (reason=%q); a latch is not claimable by any agent",
					result.BeadID, kind, result.Reason)
			}
			if result.Action != "drain" || result.Reason != hookClaimReasonNoWork {
				t.Fatalf("want action=drain reason=%s for a hook holding only a latch, got action=%q reason=%q",
					hookClaimReasonNoWork, result.Action, result.Reason)
			}
		})
	}
}

// TestHookClaimSkipsLatchAndClaimsRoutedWork is the field acceptance criterion.
// The latch does not merely burn an idle session: it sorts ahead of genuinely
// routed work on the same route, so a claim that should have landed on a real
// step landed on the latch instead. A worker offered both must be handed the
// step.
func TestHookClaimSkipsLatchAndClaimsRoutedWork(t *testing.T) {
	const (
		latchID = "infra-0co"
		stepID  = "infra-h6o4"
	)
	runner := func(string, string) (string, error) {
		return `[` +
			latchWorkQueryRow(latchID, "open", "", beadmeta.KindWorkflow, latchTestIdentity) + `,` +
			latchWorkQueryRow(stepID, "open", "", "", latchTestIdentity) +
			`]`, nil
	}
	claimed := ""
	ops := hookClaimOps{
		Runner: runner,
		Claim: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
			if id == latchID {
				t.Fatalf("store.Claim called for latch %q while routed step %q was claimable", id, stepID)
			}
			claimed = id
			return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee}, true, nil
		},
	}
	var stdout, stderr bytes.Buffer
	doHookClaim("bd ready --json", "/tmp/work", latchTestClaimOptions(), ops, &stdout, &stderr)

	result := decodeClaimResult(t, &stdout)
	if result.Action != "work" || result.BeadID != stepID {
		t.Fatalf("want the routed step %q claimed, got action=%q bead=%q reason=%q (stderr=%s)",
			stepID, result.Action, result.BeadID, result.Reason, stderr.String())
	}
	if claimed != stepID {
		t.Fatalf("claim mutation ran for %q, want %q", claimed, stepID)
	}
}

// TestHookClaimDoesNotAdoptInProgressLatch covers the adoption tier, the second
// door onto the same burn. A latch left in_progress under the POOL name matches
// every later session in that pool — GC_SESSION_NAME is the pool name — so
// hookClaimExistingAssignment would re-serve it forever without a kind test.
// This is why the filter sits ahead of all three claim tiers, not only the
// fresh-claim one.
func TestHookClaimDoesNotAdoptInProgressLatch(t *testing.T) {
	const latchID = "infra-0co"
	runner := func(string, string) (string, error) {
		return `[` + latchWorkQueryRow(latchID, "in_progress", latchTestIdentity, beadmeta.KindWorkflow, latchTestIdentity) + `]`, nil
	}
	var stdout, stderr bytes.Buffer
	doHookClaim("bd ready --json", "/tmp/work", latchTestClaimOptions(), hookClaimOps{Runner: runner}, &stdout, &stderr)

	result := decodeClaimResult(t, &stdout)
	if result.Action == "work" {
		t.Fatalf("REGRESSION infra-5b6r: hook adopted in_progress latch %q as action=work (reason=%q)", result.BeadID, result.Reason)
	}
	if result.Action != "drain" || result.Reason != hookClaimReasonNoWork {
		t.Fatalf("want action=drain reason=%s, got action=%q reason=%q", hookClaimReasonNoWork, result.Action, result.Reason)
	}
}

// TestHookClaimStillServesControlKinds pins the filter's upper bound. The
// dispatch-side skip list (cmd_convoy_dispatch.go) is WIDER than
// WorkflowTopologyKinds, and reusing it here would strand the control lane:
// workflow-finalize, drain and the rest are claimed and advanced by the
// control-dispatcher by design, and it is workflow-finalize — not the root —
// that settles the root (internal/dispatch/runtime.go processWorkflowFinalize).
// Nothing needs to claim a latch for a workflow to complete, but plenty needs to
// claim a control bead.
func TestHookClaimStillServesControlKinds(t *testing.T) {
	for _, kind := range []string{beadmeta.KindWorkflowFinalize, beadmeta.KindDrain} {
		t.Run(kind, func(t *testing.T) {
			const controlID = "infra-qf2y"
			runner := func(string, string) (string, error) {
				return `[` + latchWorkQueryRow(controlID, "open", "", kind, latchTestIdentity) + `]`, nil
			}
			ops := hookClaimOps{
				Runner: runner,
				Claim: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
					return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee}, true, nil
				},
			}
			var stdout, stderr bytes.Buffer
			doHookClaim("bd ready --json", "/tmp/work", latchTestClaimOptions(), ops, &stdout, &stderr)

			result := decodeClaimResult(t, &stdout)
			if result.Action != "work" || result.BeadID != controlID {
				t.Fatalf("control bead %q (gc.kind=%s) must stay claimable by the dispatcher lane, got action=%q bead=%q reason=%q",
					controlID, kind, result.Action, result.BeadID, result.Reason)
			}
		})
	}
}

// The old root-surfacing assertion required a worker to wake for a bead it
// could never claim. Only executable children should reach the default hook;
// the graph root remains visible through store/ready reads, not as worker work.
func TestHookSurfacesExecutableGraphChildrenNotTopology(t *testing.T) {
	for _, withChild := range []bool{false, true} {
		t.Run(fmt.Sprintf("child=%v", withChild), func(t *testing.T) {
			testHookGraphDemand(t, withChild)
		})
	}
}

func testHookGraphDemand(t *testing.T, withChild bool) {
	t.Helper()
	disableManagedDoltRecoveryForTest(t)
	clearInheritedCityRoutingEnv(t)
	cityDir := t.TempDir()
	fakeBin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cityToml := `[workspace]
name = "test-city"

[[agent]]
name = "worker"
`
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte(cityToml), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same fixture as TestCmdHookClaimsRoutedToRoot, but the root declares the
	// kind a real graph.v2 root carries (graphroute.IsCompiledGraphWorkflow
	// requires gc.kind=workflow on it).
	root := latchWorkQueryRow("graph-root", "open", "", beadmeta.KindWorkflow, "worker")
	rows := root
	if withChild {
		rows += "," + latchWorkQueryRow("graph-child", "open", "", beadmeta.KindTask, "worker")
	}
	script := `#!/bin/sh
case "$*" in
  *"--metadata-field gc.routed_to=worker"*) printf '[` + rows + `]' ;;
  *) printf '[]' ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GC_CITY", cityDir)
	t.Setenv("GC_SESSION_ORIGIN", "ephemeral")

	var stdout, stderr bytes.Buffer
	code := cmdHook([]string{"worker"}, &stdout, &stderr)
	wantCode := 1
	if withChild {
		wantCode = 0
	}
	if code != wantCode {
		t.Fatalf("cmdHook(worker) = %d, want %d; stdout=%q stderr=%s", code, wantCode, stdout.String(), stderr.String())
	}
	var got []beads.Bead
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if withChild {
		if len(got) != 1 || got[0].ID != "graph-child" {
			t.Fatalf("hook must surface the executable child only; got %s", stdout.String())
		}
	} else if len(got) != 0 {
		t.Fatalf("topology alone must not surface as executable work; got %s", stdout.String())
	}
}

// graphV2 stamps the contract that separates a graph.v2 latch from a legacy v1
// root, alongside the given kind.
func graphV2(kind string) map[string]string {
	return map[string]string{
		beadmeta.KindMetadataKey:            kind,
		beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
	}
}

// TestDropWorkflowTopologyClaimCandidates unit-pins the predicate, and in
// particular the two axes that are easy to get wrong in opposite directions:
// filtering on gc.kind alone would strand the legacy v1 workflow root (which IS
// the work), while requiring the graph.v2 contract of scope/spec would make that
// half a no-op, since gc.formula_contract rides the ROOT step only.
func TestDropWorkflowTopologyClaimCandidates(t *testing.T) {
	candidates := []beads.Bead{
		{ID: "no-metadata"},
		{ID: "empty-kind", Metadata: map[string]string{beadmeta.KindMetadataKey: ""}},
		{ID: "control", Metadata: graphV2(beadmeta.KindWorkflowFinalize)},
		// The legacy v1 root: gc.kind=workflow with no graph.v2 contract, routed
		// by gc.run_target. It is the unit of work, and must stay claimable.
		{ID: "legacy-root", Metadata: map[string]string{beadmeta.KindMetadataKey: beadmeta.KindWorkflow, beadmeta.RunTargetMetadataKey: "worker"}},
		{ID: "graphv2-root", Metadata: graphV2(beadmeta.KindWorkflow)},
		{ID: "padded-kind", Metadata: map[string]string{beadmeta.KindMetadataKey: " " + beadmeta.KindWorkflow + " ", beadmeta.FormulaContractMetadataKey: " " + beadmeta.FormulaContractGraphV2 + " "}},
		// scope/spec carry no contract in the field; filtering must not need one.
		{ID: "scope-latch", Metadata: map[string]string{beadmeta.KindMetadataKey: beadmeta.KindScope}},
		{ID: "spec-latch", Metadata: map[string]string{beadmeta.KindMetadataKey: beadmeta.KindSpec}},
	}

	got := []string{}
	for _, candidate := range dropWorkflowTopologyClaimCandidates(candidates) {
		got = append(got, candidate.ID)
	}
	want := []string{"no-metadata", "empty-kind", "control", "legacy-root"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dropWorkflowTopologyClaimCandidates kept %v, want %v", got, want)
	}
}

// TestHookClaimStillServesLegacyRunTargetRoot is the end-to-end guard for the
// same split, and the reason the predicate is not keyed on gc.kind alone. A
// legacy v1 workflow root carries gc.kind=workflow with NO graph.v2 contract and
// routes via gc.run_target; unlike a graph.v2 root it has no child steps holding
// the work, so refusing to claim it would strand the run outright rather than
// merely cost a session. Pinned upstream by
// TestDoHookClaimClaimsLegacyRunTargetWorkflowRoot; asserted here too so the
// boundary is visible from the latch filter's own test file.
func TestHookClaimStillServesLegacyRunTargetRoot(t *testing.T) {
	const legacyID = "hw-legacy"
	runner := func(string, string) (string, error) {
		return `[{"id":"` + legacyID + `","status":"open","metadata":{"gc.kind":"workflow","gc.run_target":"` + latchTestIdentity + `"}}]`, nil
	}
	ops := hookClaimOps{
		Runner: runner,
		Claim: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee}, true, nil
		},
	}
	var stdout, stderr bytes.Buffer
	doHookClaim("bd ready --json", "/tmp/work", latchTestClaimOptions(), ops, &stdout, &stderr)

	result := decodeClaimResult(t, &stdout)
	if result.Action != "work" || result.BeadID != legacyID {
		t.Fatalf("legacy v1 workflow root %q must stay claimable (it IS the work, not a latch over child steps); got action=%q bead=%q reason=%q",
			legacyID, result.Action, result.BeadID, result.Reason)
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// Focused proof command for this file:
//
//	go test ./internal/config/ -run TestLiveness -v
//
// These tests pin work item W4 (gcty-tx4v): scale_check and work_query must
// read liveness through one definition so they cannot diverge on it. The
// hazard class is the "scale_check <-> work_query protocol-mismatch" named at
// the top of workquery.go: the query that decides *whether to spawn* and the
// query that decides *what to claim* disagreeing. The live incident was
// scale_check counting a closed row as demand, spawning a session that then
// had nothing claimable to do.

// livenessTestAgent is the plain template agent both consumers are built from.
func livenessTestAgent() Agent {
	return Agent{Name: "worker", Dir: "foundations"}
}

const livenessTestTarget = "foundations/worker"

// TestLivenessPredicateIsSharedByWorkQueryAndScaleCheck is the structural
// one-definition guard. Both generators must embed the *identical* rendered
// fragment, so a future edit cannot change liveness for one consumer only.
func TestLivenessPredicateIsSharedByWorkQueryAndScaleCheck(t *testing.T) {
	a := livenessTestAgent()
	shared := beadmeta.LivenessJQSelect()

	wq := a.EffectiveWorkQuery()
	demand := a.EffectivePoolDemandQuery()

	if !strings.Contains(wq, shared) {
		t.Errorf("EffectiveWorkQuery() missing shared liveness predicate %q in %q", shared, wq)
	}
	if !strings.Contains(demand, shared) {
		t.Errorf("EffectivePoolDemandQuery() missing shared liveness predicate %q in %q", shared, demand)
	}
}

// TestLivenessClosedRowIsNotWorkAndNotDemand is the regression test for the
// incident itself. A stale ready projection can hand back a row the store has
// already closed; neither consumer may act on it.
func TestLivenessClosedRowIsNotWorkAndNotDemand(t *testing.T) {
	a := livenessTestAgent()
	bdScript := fakeBdReady(`[{"id":"fo-closed","status":"closed"}]`)

	if got := strings.TrimSpace(runEffectiveWorkQuery(t, a, nil, bdScript)); got != "[]" {
		t.Errorf("EffectiveWorkQuery() served a closed row: got %q, want []", got)
	}
	if got := strings.TrimSpace(runShellWithFakeBd(t, a.EffectivePoolDemandQuery(), nil, bdScript)); got != "0" {
		t.Errorf("EffectivePoolDemandQuery() counted a closed row as demand: got %q, want 0", got)
	}
}

// TestLivenessClosedRowIsFilteredCaseInsensitively pins that the shell filter
// normalizes exactly like the Go predicate, so the two doors cannot disagree
// on a differently-cased status spelling.
func TestLivenessClosedRowIsFilteredCaseInsensitively(t *testing.T) {
	a := livenessTestAgent()
	bdScript := fakeBdReady(`[{"id":"fo-closed","status":"CLOSED"}]`)

	if got := strings.TrimSpace(runEffectiveWorkQuery(t, a, nil, bdScript)); got != "[]" {
		t.Errorf("EffectiveWorkQuery() served a CLOSED row: got %q, want []", got)
	}
	if got := strings.TrimSpace(runShellWithFakeBd(t, a.EffectivePoolDemandQuery(), nil, bdScript)); got != "0" {
		t.Errorf("EffectivePoolDemandQuery() counted a CLOSED row: got %q, want 0", got)
	}
}

// TestLivenessMixedBatchKeepsOnlyTheLiveRow is the starvation-shaped case from
// AC-2: a genuine routed bead behind a closed one must still be served and
// still counted, so the closed row cannot consume the spawn budget or wedge
// the head of the claim loop.
func TestLivenessMixedBatchKeepsOnlyTheLiveRow(t *testing.T) {
	a := livenessTestAgent()
	bdScript := fakeBdReady(`[{"id":"fo-closed","status":"closed"},{"id":"fo-live","status":"open"}]`)

	wq := strings.TrimSpace(runEffectiveWorkQuery(t, a, nil, bdScript))
	if !strings.Contains(wq, "fo-live") {
		t.Errorf("EffectiveWorkQuery() dropped the genuine routed bead: got %q", wq)
	}
	if strings.Contains(wq, "fo-closed") {
		t.Errorf("EffectiveWorkQuery() served the closed row: got %q", wq)
	}
	if got := strings.TrimSpace(runShellWithFakeBd(t, a.EffectivePoolDemandQuery(), nil, bdScript)); got != "1" {
		t.Errorf("EffectivePoolDemandQuery() = %q, want 1 (only the live row is demand)", got)
	}
}

// TestLivenessKeepsRowsWithoutAStatusField locks the fail-open-on-absence
// choice in through the rendered shell, matching beadmeta.IsLiveStatus("").
// Fixtures and stores that omit status must not be starved.
func TestLivenessKeepsRowsWithoutAStatusField(t *testing.T) {
	a := livenessTestAgent()
	bdScript := fakeBdReady(`[{"id":"fo-routed"}]`)

	wq := strings.TrimSpace(runEffectiveWorkQuery(t, a, nil, bdScript))
	if !strings.Contains(wq, "fo-routed") {
		t.Errorf("EffectiveWorkQuery() dropped a status-less row: got %q", wq)
	}
	if got := strings.TrimSpace(runShellWithFakeBd(t, a.EffectivePoolDemandQuery(), nil, bdScript)); got != "1" {
		t.Errorf("EffectivePoolDemandQuery() = %q, want 1 for a status-less row", got)
	}
}

// TestLivenessScaleCheckStillFailsLoudOnBdError guards the invariant the
// liveness filter must not regress: a failing `bd ready` has to surface as a
// non-zero exit, never masquerade as "no demand" (which would silently stop
// the pool from spawning). See poolDemandCountShell's contract comment.
func TestLivenessScaleCheckStillFailsLoudOnBdError(t *testing.T) {
	a := livenessTestAgent()
	bdScript := "#!/bin/sh\nexit 7\n"

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "bd"), []byte(bdScript), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	cmd := testShellCommand(a.EffectivePoolDemandQuery(), tmp)
	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("EffectivePoolDemandQuery() exited 0 on bd failure, output %q; want non-zero", out)
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed == "0" {
		t.Errorf("EffectivePoolDemandQuery() printed 0 on bd failure; a store error must not read as no demand")
	}
}

// fakeBdReady builds a stub bd whose routed-ready query returns readyJSON and
// whose every other subcommand reports nothing, so a test exercises the
// canonical tier in isolation.
func fakeBdReady(readyJSON string) string {
	return `#!/bin/sh
case "$*" in
  *"ready --metadata-field ` + beadmeta.RoutedToMetadataKey + `=` + livenessTestTarget + `"*"--unassigned"*"--exclude-type=epic"*)
    printf '%s' '` + readyJSON + `'
    ;;
  *)
    printf '[]'
    ;;
esac
`
}

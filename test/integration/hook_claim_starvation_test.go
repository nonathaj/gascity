//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Focused proof command:
//
//	go test -tags integration -count=1 -timeout 10m \
//	    -run TestIntegration_HookClaimStarvation_GenuineBeadWinsOverUnclaimable \
//	    ./test/integration/
//
// This test pins REQ-010 / AC-2 of the `claim-loop-fe-bjz` build: real routed
// work must not be starved by work that cannot be claimed.
//
// Why this lives at the integration tier rather than next to the
// `hookClaimOps` / `hookClaimOptions` unit seams (plan-review finding F6):
// AC-2 requires the genuine bead be "claimed **and worked**". "Worked" means a
// live session executed it, which is not observable at the unit seam — that
// seam returns a claim result, not a running worker. Asserting it there would
// force either a weaker assertion than AC-2 requires or an unachievable one.
// Here a real session is spawned by the pool, runs the real `gc hook --claim`,
// and records what it got, so "claimed and worked" is directly observable.
//
// The failure this reproduces: the work query returns a stale projection in
// which a bead whose store row is CLOSED still looks claimable (status open,
// unassigned, route-matched). `hookCandidateClaimable` admits it,
// `claimFirstEligibleHookCandidate` claims it successfully — `bd update
// --claim` has no liveness check, so a closed row flips to in_progress — and
// the loop returns on that first success. The genuine bead, later in the same
// window, is never reached. The session then spawns, cannot act on a closed
// bead, and drains; the single pool slot is consumed and the real work is
// starved. That is the ~2-minute spawn/drain cycle reported in gcty-5e1.
//
// Expected states:
//   - Against unfixed code this FAILS: the session reports the unclaimable id.
//   - Once W4 (gcty-tx4v) lands — one liveness predicate shared by work_query
//     and scale_check — the closed row is not admitted and the genuine bead is
//     claimed and worked, so this PASSES.

const (
	// starvationUnclaimableTitle is the bead whose store row is closed but
	// which the stale work-query projection still advertises as claimable.
	starvationUnclaimableTitle = "starvation-unclaimable-closed-row"
	// starvationGenuineTitle is the real routed work that must win.
	starvationGenuineTitle = "starvation-genuine-routed-work"
	// starvationAgent is the pooled agent that races for the two beads.
	starvationAgent = "starveling"
)

// TestIntegration_HookClaimStarvation_GenuineBeadWinsOverUnclaimable asserts
// that when a genuine routed bead and an unclaimable one are present in the
// same work-query window, the single pooled session claims and works the
// genuine bead, and the unclaimable one does not consume the spawn budget.
func TestIntegration_HookClaimStarvation_GenuineBeadWinsOverUnclaimable(t *testing.T) {
	scriptDir := t.TempDir()
	candidatesPath := filepath.Join(scriptDir, "candidates.json")
	claimResultPath := filepath.Join(scriptDir, "claim-result")
	workQueryScript := filepath.Join(scriptDir, "starve-workquery.sh")
	claimScript := filepath.Join(scriptDir, "starve-claim.sh")

	writeStarvationScript(t, workQueryScript, starvationWorkQueryScript(candidatesPath))
	writeStarvationScript(t, claimScript, starvationClaimScript(claimResultPath))

	city := e2eCity{
		Agents: []e2eAgent{
			{
				Name:         starvationAgent,
				StartCommand: "bash " + claimScript,
				WorkQuery:    "bash " + workQueryScript,
				// One slot: the spawn budget REQ-010 says the unclaimable
				// bead must not consume.
				Pool: &e2ePool{Min: 1, Max: 1, Check: "echo 1"},
				Env: map[string]string{
					// The claim mutation runs through beads.NewBdStore, which
					// shells out to `bd`. Point that at the integration file
					// store so the session mutates the same rows the test
					// seeded.
					"GC_BEADS": "file",
				},
			},
		},
	}

	cityDir := setupE2ECityNoStart(t, city)

	// Seed both rows, then close one so the store disagrees with the
	// projection the work query will serve.
	unclaimableID := createStarvationBead(t, cityDir, starvationUnclaimableTitle)
	genuineID := createStarvationBead(t, cityDir, starvationGenuineTitle)
	closeStarvationBead(t, cityDir, unclaimableID)

	assertStarvationBeadStatus(t, cityDir, unclaimableID, "closed", "seeded unclaimable bead")
	assertStarvationBeadStatus(t, cityDir, genuineID, "open", "seeded genuine bead")

	// The stale projection: both rows advertised as claimable, the closed one
	// first, so an admission path without a liveness check reaches it first.
	writeStarvationCandidates(t, candidatesPath, unclaimableID, genuineID)

	// gc init spawns the pool once before the beads exist, so that session's
	// work query correctly finds nothing. Drop its record so the assertions
	// read the claim made against the seeded window, not the empty one.
	clearStarvationClaim(t, claimResultPath)

	if out, err := runGCWithEnv(commandEnvForDir(cityDir, false), "", "start", cityDir); err != nil {
		t.Fatalf("gc start failed: %v\noutput: %s", err, out)
	}

	claimed := waitForStarvationClaim(t, claimResultPath, e2eDefaultTimeout())

	// AC-2: the genuine bead is claimed and worked.
	if claimed.BeadID != genuineID {
		if claimed.BeadID == unclaimableID {
			t.Fatalf("genuine routed bead %s was STARVED: the session claimed the unclaimable closed row %s instead.\n"+
				"gc hook --claim admitted a candidate whose store row is closed and returned on that first success, "+
				"so the genuine bead later in the same window was never reached.\nclaim result: %+v",
				genuineID, unclaimableID, claimed)
		}
		t.Fatalf("session claimed %q, want genuine bead %q\nclaim result: %+v\n%s",
			claimed.BeadID, genuineID, claimed, starvationClaimDebug(claimResultPath))
	}
	if claimed.Action != "work" {
		t.Errorf("claim action = %q, want %q\nclaim result: %+v", claimed.Action, "work", claimed)
	}
	if !claimed.Worked {
		t.Errorf("genuine bead %s was claimed but not worked: no live session reached the work marker", genuineID)
	}

	// AC-2: the unclaimable bead did not consume the spawn budget. A claim
	// would have flipped its closed row to in_progress.
	assertStarvationBeadStatus(t, cityDir, unclaimableID, "closed",
		"unclaimable bead after the claim window (a non-closed status means it was claimed and consumed the spawn budget)")
}

// starvationClaim is the record a spawned session writes after running the
// real gc hook --claim.
type starvationClaim struct {
	Action string `json:"action"`
	BeadID string `json:"bead_id"`
	Reason string `json:"reason"`
	// Worked reports that the live session executed past the claim, which is
	// the half of AC-2 the unit seam cannot observe.
	Worked bool `json:"worked"`
}

// starvationWorkQueryScript renders the work query: a stale projection read
// verbatim from candidatesPath. Emitting a fixed file keeps candidate order
// deterministic, which is what makes the starvation reproducible rather than
// racy.
func starvationWorkQueryScript(candidatesPath string) string {
	return `#!/bin/bash
set -euo pipefail
if [ ! -f ` + singleQuoteShell(candidatesPath) + ` ]; then
  exit 1
fi
cat ` + singleQuoteShell(candidatesPath) + `
`
}

// starvationClaimScript renders the agent start_command: run the real claim
// path, record what came back, mark the bead as worked, then drain.
func starvationClaimScript(resultPath string) string {
	return `#!/bin/bash
set -uo pipefail
umask 000

# Capture stdout, stderr and the exit code separately. A claim that fails must
# stay diagnosable in the test failure rather than being swallowed.
stderr_file=` + singleQuoteShell(resultPath) + `.stderr
result="$(gc hook --claim --json 2>"$stderr_file")"
claim_rc=$?
{
  echo "--- gc hook --claim rc=$claim_rc ---"
  echo "--- stdout ---"
  printf '%s\n' "$result"
  echo "--- stderr ---"
  cat "$stderr_file" 2>/dev/null
  echo "--- identity ---"
  env | grep -E '^(GC_AGENT|GC_TEMPLATE|GC_SESSION_NAME|GC_SESSION_ID|GC_BEADS|GC_CITY|BEADS_ACTOR)=' | sort
} > ` + singleQuoteShell(resultPath) + `.debug 2>&1

action="$(printf '%s' "$result" | sed -n 's/.*"action"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
bead_id="$(printf '%s' "$result" | sed -n 's/.*"bead_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
reason="$(printf '%s' "$result" | sed -n 's/.*"reason"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

# "worked" is recorded only here, after the session has actually executed the
# claim and is acting on the bead it was given. This is the live-session half
# of AC-2.
worked=false
if [ -n "$bead_id" ]; then
  worked=true
fi

printf '{"action":"%s","bead_id":"%s","reason":"%s","worked":%s}\n' \
  "$action" "$bead_id" "$reason" "$worked" > ` + singleQuoteShell(resultPath) + `.tmp
mv -f ` + singleQuoteShell(resultPath) + `.tmp ` + singleQuoteShell(resultPath) + `

while true; do
  if gc runtime drain-check 2>/dev/null; then
    gc runtime drain-ack 2>/dev/null || true
    exit 0
  fi
  sleep 0.2
done
`
}

// writeStarvationCandidates writes the stale claimable projection. Both beads
// are advertised as open and unassigned — the shape hookCandidateClaimable
// admits — with the unclaimable one first.
func writeStarvationCandidates(t *testing.T, path, unclaimableID, genuineID string) {
	t.Helper()

	candidates := []map[string]any{
		starvationCandidate(unclaimableID, starvationUnclaimableTitle),
		starvationCandidate(genuineID, starvationGenuineTitle),
	}
	data, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshaling starvation candidates: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("writing starvation candidates: %v", err)
	}
}

func starvationCandidate(id, title string) map[string]any {
	return map[string]any{
		"id":         id,
		"title":      title,
		"status":     "open",
		"issue_type": "task",
		"assignee":   "",
		"metadata": map[string]string{
			"gc.routed_to": starvationAgent,
		},
	}
}

func writeStarvationScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// starvationBD runs bd against the integration file store backing cityDir.
func starvationBD(t *testing.T, cityDir string, args ...string) string {
	t.Helper()

	env := commandEnvForDir(cityDir, false)
	env = replaceEnv(env, "GC_BEADS", "file")
	env = replaceEnv(env, "GC_CITY", cityDir)
	env = replaceEnv(env, "GC_CITY_PATH", cityDir)

	out, err := runCommand(cityDir, env, 30*time.Second, bdBinary, args...)
	if err != nil {
		t.Fatalf("bd %s failed: %v\noutput: %s", strings.Join(args, " "), err, out)
	}
	return out
}

func createStarvationBead(t *testing.T, cityDir, title string) string {
	t.Helper()

	out := starvationBD(t, cityDir, "create", "--json", title)
	var created struct {
		ID string `json:"id"`
	}
	payload := strings.TrimSpace(extractJSONPayload(out))
	if err := json.Unmarshal([]byte(payload), &created); err != nil {
		t.Fatalf("unmarshaling created bead %q: %v\noutput: %s", title, err, out)
	}
	if created.ID == "" {
		t.Fatalf("created bead %q has no id\noutput: %s", title, out)
	}
	// Route it at the pooled agent so hookClaimMatchesRoute admits it.
	starvationBD(t, cityDir, "update", created.ID, "--set-metadata", "gc.routed_to="+starvationAgent)
	return created.ID
}

func closeStarvationBead(t *testing.T, cityDir, id string) {
	t.Helper()
	starvationBD(t, cityDir, "close", id)
}

func assertStarvationBeadStatus(t *testing.T, cityDir, id, want, context string) {
	t.Helper()

	out := starvationBD(t, cityDir, "show", id, "--json")
	// bd show --json emits a one-element array.
	var beads []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	payload := strings.TrimSpace(extractJSONPayload(out))
	if err := json.Unmarshal([]byte(payload), &beads); err != nil {
		t.Fatalf("unmarshaling bead %s: %v\noutput: %s", id, err, out)
	}
	if len(beads) == 0 {
		t.Fatalf("bd show %s returned no bead\noutput: %s", id, out)
	}
	if !strings.EqualFold(beads[0].Status, want) {
		t.Fatalf("%s: bead %s status = %q, want %q", context, id, beads[0].Status, want)
	}
}

// clearStarvationClaim removes any claim record left by an earlier session so
// the next one observed is the one made against the seeded window.
func clearStarvationClaim(t *testing.T, resultPath string) {
	t.Helper()
	for _, path := range []string{resultPath, resultPath + ".debug", resultPath + ".stderr"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clearing stale claim record %s: %v", path, err)
		}
	}
}

// starvationClaimDebug returns the session-side claim diagnostics (stdout,
// stderr, exit code and resolved identity) so a claim that produced nothing is
// explainable from the test output alone.
func starvationClaimDebug(resultPath string) string {
	data, err := os.ReadFile(resultPath + ".debug")
	if err != nil {
		return "session claim diagnostics unavailable: " + err.Error()
	}
	return "session claim diagnostics:\n" + string(data)
}

// waitForStarvationClaim polls for the record the spawned session writes once
// it has run gc hook --claim.
func waitForStarvationClaim(t *testing.T, path string, timeout time.Duration) starvationClaim {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			var claim starvationClaim
			if err := json.Unmarshal(data, &claim); err != nil {
				t.Fatalf("unmarshaling claim record: %v\ncontent: %s", err, data)
			}
			return claim
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for the pooled session to record a claim at %s: "+
				"no session claimed anything, so the genuine bead was never worked", timeout, path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

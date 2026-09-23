//go:build !windows

package core

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// drainTestBead is one row of a `gc bd show --json` or `gc ready --json`
// answer. The drain step reads id, status and metadata; assignee records which
// session holds each fixture bead so a scenario reads like the store it models.
type drainTestBead struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Assignee string            `json:"assignee,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// drainTestStep builds a mol-do-work step bead of the given step_ref under the
// workflow root.
func drainTestStep(id, stepRef, root, status, assignee string) drainTestBead {
	return drainTestBead{ID: id, Status: status, Assignee: assignee, Metadata: map[string]string{
		"gc.root_bead_id": root,
		"gc.step_ref":     stepRef,
	}}
}

// drainFakeGC stands in for the gc CLI that the drain step shells out to. It
// logs each call's exact argv, one call per line with arguments separated by
// \037, and accepts only the call shapes it models; anything else exits 64.
// `gc hook current --id-only` answers from GC_TEST_CURRENT_CLAIM and, like the
// real command, exits 1 when the session has claimed nothing. `gc bd show <id>
// --json` serves per-bead JSON files. `gc ready` answers from the beads the
// scenario marks ready and filters them as the real query does: a
// --metadata-field row must match its key and value exactly, a filter left out
// matches every row, and --limit applies last. An empty or repeated filter is
// not modeled. Each log entry is a single write.
const drainFakeGC = `#!/usr/bin/env bash
set -euo pipefail
printf -v entry '\037%s' "$@"
printf 'gc%s\n' "$entry" >> "$GC_TEST_LOG"
unexpected() { echo "fake gc: unmodeled call: gc $*" >&2; exit 64; }
case "$#:${1:-}:${2:-}" in
  3:hook:current)
    [ "$3" = --id-only ] || unexpected "$@"
    if [ -z "${GC_TEST_CURRENT_CLAIM:-}" ]; then
      echo "gc hook current: session ${GC_SESSION_ID:-} has no current claim (nothing claimed through gc hook --claim)" >&2
      exit 1
    fi
    printf '%s\n' "$GC_TEST_CURRENT_CLAIM"
    ;;
  4:bd:show)
    [ "$4" = --json ] || unexpected "$@"
    [ -f "$GC_TEST_BEADS/$3.json" ] || { echo "no issue found matching \"$3\"" >&2; exit 1; }
    cat "$GC_TEST_BEADS/$3.json"
    ;;
  *:bd:update)
    [ -z "${GC_TEST_FAIL_UPDATE:-}" ] || { echo "updating ${3:-}: store unavailable" >&2; exit 1; }
    ;;
  *:ready:*)
    shift
    root=""
    step=""
    limit=0
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --json|--include-ephemeral) ;;
        --limit=*) limit="${1#--limit=}" ;;
        --metadata-field)
          shift
          case "${1:-}" in
            gc.root_bead_id=?*)
              [ -z "$root" ] || unexpected ready "$@"
              root="${1#gc.root_bead_id=}"
              ;;
            gc.step_ref=?*)
              [ -z "$step" ] || unexpected ready "$@"
              step="${1#gc.step_ref=}"
              ;;
            *) unexpected ready --metadata-field "$@" ;;
          esac
          ;;
        *) unexpected ready "$@" ;;
      esac
      shift
    done
    shopt -s nullglob
    ready=("$GC_TEST_READY"/*.json)
    if [ "${#ready[@]}" -eq 0 ]; then
      echo '[]'
      exit 0
    fi
    jq -s --arg root "$root" --arg step "$step" --argjson limit "$limit" \
      '[.[] | .[] | select(($root == "" or .metadata["gc.root_bead_id"] == $root) and ($step == "" or .metadata["gc.step_ref"] == $step))] | if $limit > 0 then .[:$limit] else . end' \
      "${ready[@]}"
    ;;
  2:runtime:drain-ack) ;;
  *) unexpected "$@" ;;
esac
`

// drainFakeBD fails any direct bd call so that it cannot fall through to a bd
// on the host PATH. The drain step reaches the store only through gc.
const drainFakeBD = `#!/usr/bin/env bash
printf -v entry '\037%s' "$@"
printf 'bd%s\n' "$entry" >> "$GC_TEST_LOG"
echo "fake bd: the drain step must reach the store through gc, not bd $*" >&2
exit 64
`

// drainCloseArgv is the exact close that the drain step owes its bead, after
// `gc bd update <id>`.
var drainCloseArgv = []string{"--set-metadata", "gc.outcome=pass", "--status=closed", "--notes", "Drain acknowledged."}

// TestMolDoWorkDrainClosesOnlyThisSessionsClaim runs the drain step's shell,
// as rendered from the embedded formula, against a fake gc. Each case sets up a
// session and checks which bead the step closes and whether it acknowledges
// the drain.
//
// Pool seats are pull-based. The bead that wakes a seat, which it receives as
// GC_TRIGGER_BEAD_ID and GC_TRIGGER_WORK_BEAD_ID, is routinely not the bead
// that its `gc hook --claim` wins. The trigger can name a step that another
// live session is still working. A drain step that closes, or resolves a
// workflow from, anything other than this session's own claim can mark that
// other session's work as passed (gcty-ycjc). The claim stamp that
// `gc hook current` reads is the only session-owned answer, so the step must
// drain from it or refuse.
func TestMolDoWorkDrainClosesOnlyThisSessionsClaim(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found: %v", tool, err)
		}
	}
	script := renderMolDoWorkDrainShell(t)
	// The block must not mention a startup id in any spelling. An indirect read,
	// such as printenv or ${!name}, evades an expansion check but not this one.
	for _, startupID := range []string{"GC_BEAD_ID", "GC_TRIGGER_"} {
		if strings.Contains(script, startupID) {
			t.Errorf("drain step shell mentions %s; only this session's current claim may name its bead", startupID)
		}
	}
	// Every subtest runs these fakes, so they are written here, before any
	// subtest forks. A fake written while a sibling forks can be inherited
	// open for writing by that child, and exec then fails with ETXTBSY
	// (golang/go#22315).
	binDir := writeDrainFakes(t)

	const (
		self  = "sess-self"
		other = "sess-other"
	)
	var (
		ownWork     = drainTestStep("gc-own-work", "mol-do-work.do-work", "gc-own-root", "closed", self)
		ownWorkOpen = drainTestStep("gc-own-work", "mol-do-work.do-work", "gc-own-root", "in_progress", self)
		ownDrain    = drainTestStep("gc-own-drain", "mol-do-work.drain", "gc-own-root", "open", "")
		ownClaimed  = drainTestStep("gc-own-drain", "mol-do-work.drain", "gc-own-root", "in_progress", self)
		ownDrained  = drainTestStep("gc-own-drain", "mol-do-work.drain", "gc-own-root", "closed", self)
		ownDrainDup = drainTestStep("gc-own-drain-dup", "mol-do-work.drain", "gc-own-root", "open", "")
		// ownOtherStep is a ready step of the claimed workflow that is not its
		// drain step. It catches a ready query that drops its step_ref filter.
		ownOtherStep = drainTestStep("gc-own-other-step", "mol-do-work.other-step", "gc-own-root", "open", "")
		// otherWork is the live shape from the report: a do-work step that another
		// session holds in progress, named by this seat's trigger.
		otherWork  = drainTestStep("gc-other-work", "mol-do-work.do-work", "gc-other-root", "in_progress", other)
		otherDrain = drainTestStep("gc-other-drain", "mol-do-work.drain", "gc-other-root", "open", "")
		looseClaim = drainTestBead{ID: "gc-loose", Status: "in_progress", Assignee: self}
	)
	// foreignStartupIDs points every startup id a session shell can carry at
	// another session's in-progress step.
	foreignStartupIDs := map[string]string{
		"GC_BEAD_ID":              otherWork.ID,
		"GC_TRIGGER_BEAD_ID":      otherWork.ID,
		"GC_TRIGGER_WORK_BEAD_ID": otherWork.ID,
	}

	cases := []struct {
		name         string
		beads        []drainTestBead
		ready        []drainTestBead
		currentClaim string
		env          map[string]string
		failUpdate   bool
		wantUpdated  []string
		wantDrainAck bool
		wantFail     bool
	}{
		{
			// The reported defect: the seat claimed its own drain step while its
			// trigger names a step that another session holds in progress.
			name:         "claimed drain step wins over a foreign trigger",
			beads:        []drainTestBead{ownClaimed, otherWork, otherDrain},
			ready:        []drainTestBead{otherDrain},
			currentClaim: ownClaimed.ID,
			env:          foreignStartupIDs,
			wantUpdated:  []string{ownClaimed.ID},
			wantDrainAck: true,
		},
		{
			// The same session carries on from its closed do-work claim to the
			// drain step of the same workflow, without a fresh claim.
			name:         "deferred continuation drains the claimed workflow, not the trigger's",
			beads:        []drainTestBead{ownWork, ownDrain, otherWork, otherDrain},
			ready:        []drainTestBead{ownDrain, otherDrain},
			currentClaim: ownWork.ID,
			env:          foreignStartupIDs,
			wantUpdated:  []string{ownDrain.ID},
			wantDrainAck: true,
		},
		{
			// With no claim to name, no startup id is a safe substitute: each
			// names a bead this session does not own.
			name:         "no current claim refuses instead of falling back to startup ids",
			beads:        []drainTestBead{otherWork, otherDrain},
			ready:        []drainTestBead{otherDrain},
			currentClaim: "",
			env:          foreignStartupIDs,
			wantFail:     true,
		},
		{
			name:         "a claim outside any workflow refuses instead of falling back to startup ids",
			beads:        []drainTestBead{looseClaim, otherWork, otherDrain},
			ready:        []drainTestBead{otherDrain},
			currentClaim: looseClaim.ID,
			env:          foreignStartupIDs,
			wantFail:     true,
		},
		{
			// An unclosed do-work step keeps the drain blocked, so nothing is
			// ready to drain yet. The ready rows tempt a query that drops either
			// of its metadata filters.
			name:         "an unfinished do-work claim refuses to drain",
			beads:        []drainTestBead{ownWorkOpen, ownDrain, ownOtherStep, otherWork, otherDrain},
			ready:        []drainTestBead{ownOtherStep, otherDrain},
			currentClaim: ownWorkOpen.ID,
			env:          foreignStartupIDs,
			wantFail:     true,
		},
		{
			name:         "two ready drain steps for the claimed workflow refuse to guess",
			beads:        []drainTestBead{ownWork, ownDrain, ownDrainDup, otherWork, otherDrain},
			ready:        []drainTestBead{ownDrain, ownDrainDup, otherDrain},
			currentClaim: ownWork.ID,
			env:          foreignStartupIDs,
			wantFail:     true,
		},
		{
			// A rerun after a drain-ack that failed finds the claimed drain step
			// already closed. The step never closes a bead twice, and the formula
			// tells the agent to retry only the drain-ack.
			name:         "a claimed drain step that is already closed refuses to close it again",
			beads:        []drainTestBead{ownDrained, otherWork, otherDrain},
			ready:        []drainTestBead{otherDrain},
			currentClaim: ownDrained.ID,
			env:          foreignStartupIDs,
			wantFail:     true,
		},
		{
			name:         "a failed close withholds the drain acknowledgement",
			beads:        []drainTestBead{ownClaimed, otherWork, otherDrain},
			ready:        []drainTestBead{otherDrain},
			currentClaim: ownClaimed.ID,
			env:          foreignStartupIDs,
			failUpdate:   true,
			wantUpdated:  []string{ownClaimed.ID},
			wantFail:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := runMolDoWorkDrain(t, script, binDir, drainRun{
				session:      self,
				beads:        tc.beads,
				ready:        tc.ready,
				currentClaim: tc.currentClaim,
				env:          tc.env,
				failUpdate:   tc.failUpdate,
			})
			if !run.called("gc", "hook", "current", "--id-only") {
				t.Errorf("drain never read this session's claim with `gc hook current --id-only`\n%s", run)
			}
			// runMolDoWorkDrain has already failed the test on any unmodeled
			// call, so every update here names a bead.
			var updated []string
			for _, call := range run.calls {
				if len(call) < 3 || call[0] != "gc" || call[1] != "bd" || call[2] != "update" {
					continue
				}
				updated = append(updated, call[3])
				if !slices.Equal(call[4:], drainCloseArgv) {
					t.Errorf("update of %s used argv %q, want %q\n%s", call[3], call[4:], drainCloseArgv, run)
				}
			}
			if !slices.Equal(updated, tc.wantUpdated) {
				t.Errorf("updated beads %q, want %q\n%s", updated, tc.wantUpdated, run)
			}
			if got := run.called("gc", "runtime", "drain-ack"); got != tc.wantDrainAck {
				t.Errorf("drain-ack called = %t, want %t\n%s", got, tc.wantDrainAck, run)
			}
			if failed := run.err != nil; failed != tc.wantFail {
				t.Errorf("step failed = %t (%v), want %t\n%s", failed, run.err, tc.wantFail, run)
			}
			// A refusal happens before any close, and it must name the step so
			// that an agent reading the error knows what declined.
			if tc.wantFail && len(tc.wantUpdated) == 0 && !strings.Contains(run.stderr, "mol-do-work drain") {
				t.Errorf("refusal does not name the mol-do-work drain step\n%s", run)
			}
		})
	}
}

// drainRun is one execution of the drain step against the fake gc.
type drainRun struct {
	session      string
	beads        []drainTestBead
	ready        []drainTestBead
	currentClaim string
	env          map[string]string
	failUpdate   bool
}

// drainResult is what one drain step execution did, as the fakes saw it.
type drainResult struct {
	calls  [][]string
	stdout string
	stderr string
	err    error
}

// called reports whether the step made exactly this call.
func (r drainResult) called(argv ...string) bool {
	return slices.ContainsFunc(r.calls, func(call []string) bool { return slices.Equal(call, argv) })
}

// String renders the calls and output for a failure message.
func (r drainResult) String() string {
	var b strings.Builder
	b.WriteString("calls:\n")
	for _, call := range r.calls {
		b.WriteString("  " + strings.Join(call, " ") + "\n")
	}
	b.WriteString("stdout:\n" + r.stdout + "\nstderr:\n" + r.stderr)
	return b.String()
}

// writeDrainFakes writes the fake gc and bd into a directory that every
// subtest shares, and returns that directory.
func writeDrainFakes(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	writeDrainTestFile(t, filepath.Join(binDir, "gc"), drainFakeGC, 0o755)
	writeDrainTestFile(t, filepath.Join(binDir, "bd"), drainFakeBD, 0o755)
	return binDir
}

// runMolDoWorkDrain executes the drain step once. The session's store lives in
// a private directory, its environment is built from scratch rather than
// inherited, and the fakes in binDir come first on PATH. It fails the test when
// the step makes a call that neither fake models.
func runMolDoWorkDrain(t *testing.T, script, binDir string, in drainRun) drainResult {
	t.Helper()
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, "beads")
	readyDir := filepath.Join(dir, "ready")
	for _, d := range []string{beadsDir, readyDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("Mkdir(%s): %v", d, err)
		}
	}
	for _, b := range in.beads {
		writeDrainTestBead(t, beadsDir, b)
	}
	for _, b := range in.ready {
		writeDrainTestBead(t, readyDir, b)
	}
	logPath := filepath.Join(dir, "calls.log")

	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + dir,
		"GC_SESSION_ID=" + in.session,
		"GC_TEST_LOG=" + logPath,
		"GC_TEST_BEADS=" + beadsDir,
		"GC_TEST_READY=" + readyDir,
		"GC_TEST_CURRENT_CLAIM=" + in.currentClaim,
	}
	if in.failUpdate {
		env = append(env, "GC_TEST_FAIL_UPDATE=1")
	}
	for k, v := range in.env {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command("bash", "-s")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	result := drainResult{stdout: stdout.String(), stderr: stderr.String(), err: runErr}
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading call log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line != "" {
			result.calls = append(result.calls, strings.Split(line, "\x1f"))
		}
	}
	for _, call := range result.calls {
		if !drainCallModeled(call) {
			t.Fatalf("drain step made a call the fakes do not model: %q\n%s", call, result)
		}
	}
	return result
}

// drainCallModeled reports whether call has one of the shapes that the fake gc
// answers faithfully. The check reads the call log, not stderr, so a step that
// hides its errors still fails on any other call, including any direct bd call.
func drainCallModeled(call []string) bool {
	if len(call) < 2 || call[0] != "gc" {
		return false
	}
	args := call[1:]
	switch {
	case slices.Equal(args, []string{"hook", "current", "--id-only"}),
		slices.Equal(args, []string{"runtime", "drain-ack"}):
		return true
	case len(args) == 4 && args[0] == "bd" && args[1] == "show" && args[3] == "--json":
		return true
	case len(args) >= 3 && args[0] == "bd" && args[1] == "update":
		return true
	case args[0] == "ready":
		return drainReadyArgsModeled(args[1:])
	}
	return false
}

// drainReadyArgsModeled reports whether the fake gc interprets every argument
// of a `gc ready` call. Each metadata filter must name gc.root_bead_id or
// gc.step_ref, once, with a non-empty value.
func drainReadyArgsModeled(args []string) bool {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--json", arg == "--include-ephemeral", strings.HasPrefix(arg, "--limit="):
		case arg == "--metadata-field" && i+1 < len(args):
			key, value, _ := strings.Cut(args[i+1], "=")
			if (key != "gc.root_bead_id" && key != "gc.step_ref") || value == "" || seen[key] {
				return false
			}
			seen[key] = true
			i++
		default:
			return false
		}
	}
	return true
}

// renderMolDoWorkDrainShell returns the shell an agent runs for the drain step:
// the first bash fence of the step's description in the embedded formula.
func renderMolDoWorkDrainShell(t *testing.T) string {
	t.Helper()
	desc := formulaStep(t, readFormula(t, "mol-do-work.toml"), "drain")
	start := strings.Index(desc, "```bash\n")
	if start < 0 {
		t.Fatal("drain step has no bash fence")
	}
	body := desc[start+len("```bash\n"):]
	end := strings.Index(body, "\n```")
	if end < 0 {
		t.Fatal("drain step bash fence is not closed")
	}
	script := body[:end+1]
	if strings.Contains(script, "{{") {
		t.Fatalf("drain step shell has an unrendered template variable:\n%s", script)
	}
	return script
}

// writeDrainTestBead writes b as the one-row array that `gc bd show --json`
// returns, named for its id.
func writeDrainTestBead(t *testing.T, dir string, b drainTestBead) {
	t.Helper()
	data, err := json.Marshal([]drainTestBead{b})
	if err != nil {
		t.Fatalf("marshal %s: %v", b.ID, err)
	}
	writeDrainTestFile(t, filepath.Join(dir, b.ID+".json"), string(data), 0o644)
}

// writeDrainTestFile writes a fixture file with the given mode.
func writeDrainTestFile(t *testing.T, path, data string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), perm); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

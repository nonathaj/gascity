package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/execshim"
)

// TestVerifyOurServerAcceptsWhenStateFileAgrees exercises the REAL verify_our_server.
//
// Every existing test that touches the adopt/stop paths stubs this function out
// (`verify_our_server() { return 0; }` and a pid-equality stub elsewhere in
// beads_provider_lifecycle_test.go), so the actual identity check has never been
// covered. That is the same failure shape as the vacuous pid assertions fixed under
// gw-591: the harness replaced the thing under test. This test therefore overrides only
// the OS-probe leaves it must, and never verify_our_server itself.
//
// The contract: given a state file whose data_dir matches DATA_DIR, and no evidence to
// the contrary, a live pid must be judged OURS. That is exactly what the function's own
// closing comment intends —
//
//	# State file said it's ours (or no state file) and we couldn't disprove it.
//
// On Windows that block is unreachable, because Layer 2 runs `ps -p <pid> -o args=` and
// Git for Windows' ps rejects `-o` ("unknown option -- o"), so the `|| return 1` on that
// line fires for every pid in any numbering space. Result: ensure-ready never adopts and
// stop never kills (gw-1ay).
//
// Platform-neutral: on Linux ps answers but a stand-in process matches none of the
// --config/--data-dir patterns, so control also reaches the state-file fallback and the
// expected answer is the same.
func TestVerifyOurServerAcceptsWhenStateFileAgrees(t *testing.T) {
	skipSlowCmdGCTest(t, "spawns a real child process through sh; run make test-cmd-gc-process for full coverage")

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	stateFile := filepath.Join(dir, "dolt-state.json")
	configFile := filepath.Join(dir, "dolt-config.yaml")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The state file is the evidence the function is supposed to trust when nothing
	// disproves it. data_dir must match DATA_DIR for Layer 1 to pass.
	stateJSON := fmt.Sprintf(`{"running":true,"pid":0,"port":1,"data_dir":%q,"started_at":"2026-04-14T00:00:00Z"}`,
		filepath.ToSlash(dataDir))
	if err := os.WriteFile(stateFile, []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `
DATA_DIR=` + shScriptPath(dataDir) + `
STATE_FILE=` + shScriptPath(stateFile) + `
CONFIG_FILE=` + shScriptPath(configFile) + `
sleep 60 >/dev/null 2>&1 & # interpreter-local pid: only this shell inspects it
probe_pid=$!
if verify_our_server "$probe_pid"; then
  printf 'verdict=ours\n'
else
  printf 'verdict=not-ours\n'
fi
kill "$probe_pid" 2>/dev/null || true
`
	cmd := execshim.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(pidSpacePrelude(t) + "\n" + body)
	cmd.Env = sanitizedBaseEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify_our_server harness failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "verdict=ours") {
		t.Fatalf("verify_our_server judged a live process NOT ours even though the state "+
			"file's data_dir matches DATA_DIR and nothing disproved ownership.\n"+
			"On Windows this is the `ps -p -o args=` hard-fail at gc-beads-bd.sh:1015 "+
			"making the state-file fallback unreachable, so ensure-ready never adopts and "+
			"stop never kills (gw-1ay).\noutput:\n%s", out)
	}
}

// TestVerifyOurServerRejectsForeignDataDir is the reject-direction guard that runs
// EVERYWHERE, including Windows.
//
// Layer 1 (state data_dir vs DATA_DIR) needs no process introspection, so it is assertable
// on any OS with a real process. The args-based imposter test below covers Layer 2 on every
// platform by stubbing ps; this one keeps a reject-direction guard that runs against a
// genuine live process rather than a stubbed probe.
func TestVerifyOurServerRejectsForeignDataDir(t *testing.T) {
	skipSlowCmdGCTest(t, "spawns a real child process through sh; run make test-cmd-gc-process for full coverage")

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	otherDir := filepath.Join(dir, "someone-elses-data")
	stateFile := filepath.Join(dir, "dolt-state.json")
	for _, d := range []string{dataDir, otherDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// State file names a DIFFERENT data dir: positive evidence this pid is not ours.
	stateJSON := fmt.Sprintf(`{"running":true,"pid":0,"port":1,"data_dir":%q,"started_at":"2026-04-14T00:00:00Z"}`,
		filepath.ToSlash(otherDir))
	if err := os.WriteFile(stateFile, []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `
DATA_DIR=` + shScriptPath(dataDir) + `
STATE_FILE=` + shScriptPath(stateFile) + `
CONFIG_FILE=` + shScriptPath(filepath.Join(dir, "ours.yaml")) + `
sleep 60 >/dev/null 2>&1 & # interpreter-local pid: only this shell inspects it
probe_pid=$!
if verify_our_server "$probe_pid"; then
  printf 'verdict=ours\n'
else
  printf 'verdict=not-ours\n'
fi
kill "$probe_pid" 2>/dev/null || true
`
	cmd := execshim.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(pidSpacePrelude(t) + "\n" + body)
	cmd.Env = sanitizedBaseEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("foreign-data-dir harness failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "verdict=not-ours") {
		t.Fatalf("verify_our_server claimed a process is ours while the state file names a "+
			"different data_dir; the Layer 2 relaxation must not degrade into always-ours "+
			"(gw-1ay)\noutput:\n%s", out)
	}
}

// TestVerifyOurServerRejectsForeignConfig pins the other direction, so the fix above
// cannot degrade into "always ours". A process whose args name a DIFFERENT --config is
// an imposter and must be rejected even though the state file agrees.
//
// ps is stubbed rather than a real process being spawned and inspected. That is what lets
// this run on EVERY platform, including the one it is really about: Git for Windows' ps
// cannot report args at all, so the real-process version skipped on Windows — the exact
// platform whose ps limitation caused gw-1ay. A test that skips where the bug lives is
// not covering it.
//
// The subject here is verify_our_server's DECISION, not the platform's ps.
func TestVerifyOurServerRejectsForeignConfig(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	stateFile := filepath.Join(dir, "dolt-state.json")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The state file AGREES, so only the args evidence can produce a rejection. Without
	// that agreement Layer 1 would reject and the test would pass without ever reaching
	// the args check it exists to cover.
	stateJSON := fmt.Sprintf(`{"running":true,"pid":0,"port":1,"data_dir":%q,"started_at":"2026-04-14T00:00:00Z"}`,
		filepath.ToSlash(dataDir))
	if err := os.WriteFile(stateFile, []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name     string
		procArgs string
		want     string
	}{{
		name:     "foreign config is an imposter",
		procArgs: "dolt sql-server --config /somewhere/else/theirs.yaml",
		want:     "verdict=not-ours",
	}, {
		name:     "our own config is ours",
		procArgs: "dolt sql-server --config CONFIG_FILE_PLACEHOLDER",
		want:     "verdict=ours",
	}, {
		name:     "foreign data-dir is an imposter",
		procArgs: "dolt sql-server --data-dir /somewhere/else/data",
		want:     "verdict=not-ours",
	}, {
		name:     "no args available falls through to the state file",
		procArgs: "",
		want:     "verdict=ours",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			configFile := filepath.Join(dir, "ours.yaml")
			procArgs := strings.ReplaceAll(tc.procArgs, "CONFIG_FILE_PLACEHOLDER", filepath.ToSlash(configFile))
			body := `
DATA_DIR=` + shScriptPath(dataDir) + `
STATE_FILE=` + shScriptPath(stateFile) + `
CONFIG_FILE=` + shScriptPath(configFile) + `
# Stand in for the platform's ps. Overriding the command is what makes the args evidence
# available on hosts whose real ps cannot supply it.
ps() { printf '%s\n' ` + shScriptPath(procArgs) + `; }
if verify_our_server 4242; then
  printf 'verdict=ours\n'
else
  printf 'verdict=not-ours\n'
fi
`
			cmd := execshim.Command("sh", "-s")
			cmd.Stdin = strings.NewReader(pidSpacePrelude(t) + "\n" + body)
			cmd.Env = sanitizedBaseEnv()
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("imposter harness failed: %v\noutput:\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("verify_our_server verdict for args %q = %s, want %s. The accept path "+
					"must not degrade into always-ours, and the reject path must not fire on "+
					"absent evidence (gw-1ay).\noutput:\n%s",
					procArgs, strings.TrimSpace(string(out)), tc.want, out)
			}
		})
	}
}

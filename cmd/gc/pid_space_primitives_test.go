package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	bdpack "github.com/gastownhall/gascity/examples/bd"
	"github.com/gastownhall/gascity/internal/execshim"
	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

// pidSpacePrelude returns the pack script's function definitions, so the boundary
// primitives (native_pid_of, pid_alive, pid_kill) can be exercised directly.
func pidSpacePrelude(t *testing.T) string {
	t.Helper()
	embedded, err := bdpack.PackFS.ReadFile("assets/scripts/gc-beads-bd.sh")
	if err != nil {
		t.Fatalf("read embedded gc-beads-bd.sh: %v", err)
	}
	prelude, _, found := strings.Cut(string(embedded), "# --- Main ---")
	if !found {
		t.Fatal("embedded gc-beads-bd.sh is missing the main boundary")
	}
	return prelude
}

// runPidSpaceScript runs body with the script's prelude sourced and returns stdout.
func runPidSpaceScript(t *testing.T, body string) string {
	t.Helper()
	cmd := execshim.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(pidSpacePrelude(t) + "\n" + body)
	cmd.Env = sanitizedBaseEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pid-space script failed: %v\noutput:\n%s", err, out)
	}
	return string(out)
}

// TestPidSpacePrimitivesRoundTripAcrossTheBoundary covers plan items #4 and #5 from
// engdocs/contributors/windows-pid-space.md: the value one sh invocation persists must
// be recognized by Go's probe, and a LATER sh invocation must be able to terminate it.
//
// That two-invocation shape is the real stop path — `gc dolt stop` and the script's own
// stop op both act on a pid recorded by an earlier process — and it is the case MSYS
// `kill` alone cannot serve, which is the whole reason pid_kill exists.
//
// Liveness and termination are asserted separately on purpose. gw-1oa was a broken
// probe, gw-dbm a broken value, and a wrong kill target is a third failure mode; a
// suite that only checks "is it alive" would miss the last one.
//
// Platform-neutral: on Unix native_pid_of is identity and `kill` handles everything, so
// the same assertions hold without build tags.
func TestPidSpacePrimitivesRoundTripAcrossTheBoundary(t *testing.T) {
	skipSlowCmdGCTest(t, "spawns a real child process through sh; run make test-cmd-gc-process for full coverage")

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "native.pid")

	// Invocation 1: spawn, convert at capture exactly as the start path does, persist.
	// The child's stdio MUST be detached. Inheriting it keeps the pipe open, so
	// CombinedOutput blocks until the child exits — the first run of this test took
	// 61s and then reported the pid dead, because by the time Go looked, the sleep had
	// legitimately finished. Same shape as the grandchild-holds-the-pipe problem in
	// gw-ho3.
	out := runPidSpaceScript(t, `
sleep 60 >/dev/null 2>&1 & # interpreter-local pid: converted below before it is persisted
shell_pid=$!
native_pid=$(native_pid_of "$shell_pid") || native_pid="$shell_pid"
[ -n "$native_pid" ] || native_pid="$shell_pid"
printf '%s\n' "$native_pid" > `+shScriptPath(pidFile)+`
printf 'alive_per_sh=%s\n' "$(pid_alive "$native_pid" && echo yes || echo no)"
`)
	if !strings.Contains(out, "alive_per_sh=yes") {
		t.Fatalf("sh pid_alive did not recognize the pid it just converted:\n%s", out)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read persisted native pid: %v", err)
	}
	nativePID, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse persisted native pid %q: %v", strings.TrimSpace(string(raw)), err)
	}
	t.Cleanup(func() { _ = platformKill(nativePID, 9) })

	// #5: both sides of the boundary must agree the process is ALIVE.
	if !pidutil.Alive(nativePID) {
		t.Fatalf("pid %d persisted by sh is not live per pidutil.Alive; the two sides of "+
			"the boundary disagree about the same process (gw-dbm)", nativePID)
	}

	// #4: a LATER sh invocation must be able to terminate it — the stop path.
	runPidSpaceScript(t, `pid_kill `+strconv.Quote(strconv.Itoa(nativePID))+` force`+"\n")

	if testutil.WaitUntil(15*time.Second, func() bool { return !pidutil.Alive(nativePID) }) {
		return
	}
	t.Fatalf("pid_kill did not terminate pid %d; MSYS kill cannot signal a native pid, "+
		"so the taskkill fallback is what has to work here (gw-dbm)", nativePID)
}

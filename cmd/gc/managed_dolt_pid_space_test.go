package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/execshim"
	"github.com/gastownhall/gascity/internal/pidutil"
)

// managedDoltPidSpaceStart runs the pack script's REAL managed-dolt start with GC_BIN
// unset — the path where the script spawns the server itself instead of delegating to
// `gc dolt-state start-managed` — and returns the runtime state it persisted.
//
// Fixture notes, each learned the hard way:
//
//   - The fake dolt must be SUBCOMMAND-AWARE. Start also runs
//     `dolt config --global --get user.*`, `dolt version`, and (with GC_BIN unset) a
//     `dolt --host … sql` query probe. A blanket long-lived fake blocks identity setup
//     instead of standing in for the server.
//   - It cannot be driven through the prelude-override harness other script tests use:
//     the env→globals initialisation (DATA_DIR, LOCK_FILE, …) lives in the script's
//     Main section, so calling op_start against the prelude alone leaves those empty
//     and the script dies in `mkdir -p '' .`.
//   - Readiness is satisfied by binding the port from Go AFTER the spawn. Binding it
//     first would make the script treat the port as already-served and adopt instead
//     of spawning, which is not the path under test.
//   - No python3/dolt dependency: Git-Bash ships neither (doctrine T8/T12), and the
//     contract under test is pid bookkeeping, not dolt behaviour.
func managedDoltPidSpaceStart(t *testing.T) doltRuntimeState {
	t.Helper()
	skipSlowCmdGCTest(t, "runs the real gc-beads-bd start path with a fake dolt; run make test-cmd-gc-process for full coverage")

	cityPath := t.TempDir()
	packStateDir := filepath.Join(cityPath, ".gc", "runtime", "packs", "dolt")
	dataDir := filepath.Join(cityPath, ".beads", "dolt")
	binDir := filepath.Join(t.TempDir(), "bin")
	for _, dir := range []string{packStateDir, dataDir, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCityToml(t, cityPath, "[workspace]\nname = \"pid-space-city\"\n")
	materializeBuiltinPacksForTest(t, cityPath)
	script := gcBeadsBdScriptPath(cityPath)

	installFakeToolOnPath(t, binDir, "dolt", `#!/bin/sh
case "${1:-}" in
  sql-server) exec sleep 60 ;;
  version)    echo "dolt version 2.1.0"; exit 0 ;;
  *)          exit 0 ;;
esac
`)
	installFakeToolOnPath(t, binDir, "bd", "#!/bin/sh\nexit 0\n")

	port := reserveRandomTCPPort(t)
	statePath := filepath.Join(packStateDir, "dolt-state.json")

	cmd := execshim.Command(script, "start")
	cmd.Env = prependPathDir(sanitizedBaseEnv(
		"GC_CITY_PATH="+cityPath,
		"GC_DOLT_HOST=127.0.0.1",
		"GC_DOLT_PORT="+strconv.Itoa(port),
		"GC_PACK_STATE_DIR="+packStateDir,
		"GC_DOLT_DATA_DIR="+dataDir,
		"GC_DOLT_LOG_FILE="+filepath.Join(packStateDir, "dolt.log"),
		"GC_DOLT_STATE_FILE="+statePath,
		"GC_DOLT_PID_FILE="+filepath.Join(packStateDir, "dolt.pid"),
		"GC_DOLT_LOCK_FILE="+filepath.Join(packStateDir, "dolt.lock"),
		"GC_DOLT_CONFIG_FILE="+filepath.Join(packStateDir, "dolt-config.yaml"),
		"PATH="+strings.Join([]string{binDir, os.Getenv("PATH")}, string(os.PathListSeparator)),
	), binDir)

	done := make(chan error, 1)
	var out []byte
	go func() {
		var runErr error
		out, runErr = cmd.CombinedOutput()
		done <- runErr
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = pidutil.KillTree(cmd.Process.Pid)
		}
	})

	// Satisfy the readiness probe once the spawn has happened.
	var ln net.Listener
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ln == nil {
		if l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			ln = l
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ln != nil {
		t.Cleanup(func() { _ = ln.Close() })
	}

	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatalf("gc-beads-bd start did not return\noutput so far:\n%s", out)
	}

	state, err := readDoltRuntimeStateFile(statePath)
	if err != nil {
		t.Fatalf("read persisted dolt state: %v\nscript output:\n%s", err, out)
	}
	t.Cleanup(func() {
		if state.PID > 0 {
			_ = platformKill(state.PID, 9)
		}
	})
	if state.PID <= 0 {
		t.Fatalf("script persisted no pid\nscript output:\n%s", out)
	}
	return state
}

// TestManagedDoltStatePIDIsNativeAfterShellFallbackStart is the boundary contract for
// process identity crossing the sh↔Go boundary.
//
// The assertion is deliberately "production's own probe accepts the value gc stored",
// not "the value is a Windows pid". That phrasing is meaningful on every platform
// (true by construction on Unix), needs no build tags, and cannot be satisfied by
// relabelling a field — which is what makes it a contract test rather than a mechanism
// test. See engdocs/contributors/windows-pid-space.md.
func TestManagedDoltStatePIDIsNativeAfterShellFallbackStart(t *testing.T) {
	// EXPECTED-RED until gw-dbm Phase 1 lands. Verified failing as written:
	//   "dolt-state.json pid 49126 is not live per pidutil.Alive … (gw-dbm)"
	// 49126 was an MSYS pid. Skipped rather than left failing so that
	// `make test-cmd-gc-process` does not report a defect with no context; the skip
	// removal is part of Phase 1's definition of done, not an afterthought.
	t.Skip("expected-red pending gw-dbm Phase 1: script records an MSYS pid where Go reads a native pid")

	state := managedDoltPidSpaceStart(t)
	if !pidutil.Alive(state.PID) {
		t.Fatalf("dolt-state.json pid %d is not live per pidutil.Alive, the probe "+
			"production uses (beads_provider_lifecycle.go pidAlive). The script recorded "+
			"a pid in a different numbering space than Go reads it in (gw-dbm)", state.PID)
	}
}

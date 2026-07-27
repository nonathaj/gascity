package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

const (
	// doltStandInPortFileEnv names the file the stand-in writes its bound port to.
	doltStandInPortFileEnv = "GC_TEST_DOLT_STANDIN_PORT_FILE"
	// doltStandInHelperPattern selects the helper when the binary re-executes itself.
	doltStandInHelperPattern = "^TestDoltStandInHelper$"
)

// TestDoltStandInHelper is not a test. It is the process a fixture spawns to stand in for a
// managed dolt server, and it skips unless the environment marks it as the helper.
//
// It exists because ownership inspection reads a process's ARGV to decide whether a pid is our
// dolt (does it name our --config?), and it compares the pid holding the dolt port against the
// pid in dolt-state.json. A faithful stand-in therefore has to do both things at once: hold the
// port AND carry dolt-like argv. os.Getpid() cannot satisfy the second — a test binary's argv
// is fixed — which is why fixtures using it only ever passed through a pid-equality shortcut
// that skipped inspection entirely.
//
// The listening and blocking live in internal/testutil rather than here. The resource census
// counts listeners and sleeps in _test.go files on purpose, so shared patterns migrate into
// reviewed library code, and net_listen's baseline is additionally pinned by a policy test.
func TestDoltStandInHelper(t *testing.T) {
	portFile := strings.TrimSpace(os.Getenv(doltStandInPortFileEnv))
	if portFile == "" {
		t.Skip("subprocess helper; runs only when the parent marks it")
	}
	listener := testutil.ListenAndReportPort(t, portFile)
	defer func() { _ = listener.Close() }()
	testutil.HoldUntilKilled()
}

// doltStandIn describes a spawned stand-in for managed dolt.
type doltStandIn struct {
	PID  int
	Port int
}

// startDoltStandInForConfig spawns a process that stands in for managed dolt: it holds a TCP
// port and carries `--config <configFile>` in its argv, so managedDoltRuntimeProcessOwned can
// verify ownership instead of taking a pid match on trust.
func startDoltStandInForConfig(t *testing.T, configFile string) doltStandIn {
	t.Helper()
	portFile := filepath.Join(t.TempDir(), "standin.port")
	cmd := testutil.StartSelfHelper(t, doltStandInHelperPattern,
		[]string{doltStandInPortFileEnv + "=" + portFile},
		"sql-server", "--config="+filepath.ToSlash(configFile),
	)
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = pidutil.KillTree(pid)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	go func() { _ = cmd.Wait() }()

	return doltStandIn{PID: pid, Port: testutil.ReadReportedPort(t, portFile, 30*time.Second)}
}

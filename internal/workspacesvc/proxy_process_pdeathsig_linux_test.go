//go:build linux

package workspacesvc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This test lives in a linux file because it pins the Pdeathsig guarantee, and
// Pdeathsig is a Linux-only kernel feature: applyProxyProcessPdeathsig is a
// no-op on every other platform (see proxy_process_other.go), so there is
// nothing here to assert off Linux. Its mechanics are Linux-shaped too —
// syscall.Kill(pid, 0) as a liveness probe has no Windows equivalent.

// TestProxyProcessSurvivesHardParentExit is the RED test for ga-9br097's
// Family A acceptance criterion: a proxy_process child spawned by start()
// must not survive its parent's hard exit (the Go -timeout watchdog's
// os.Exit path, which runs no defer/t.Cleanup anywhere in the process).
// It re-execs this test binary as a harness (TestProxyProcessHardExitHarness)
// that starts a real child and then os.Exit(1)s with zero cleanup, then
// asserts the grandchild is gone. start() does not set Pdeathsig today, so
// this must fail.
func TestProxyProcessSurvivesHardParentExit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	stateDir := t.TempDir()
	pidFile := filepath.Join(stateDir, "grandchild.pid")

	cmd := exec.Command(exe, "-test.run=^TestProxyProcessHardExitHarness$", "--")
	cmd.Env = append(os.Environ(),
		"GC_HARD_EXIT_HARNESS=1",
		"GC_SERVICE_HELPER=1",
		"GC_HARD_EXIT_CITYDIR="+stateDir,
		"GC_HARD_EXIT_PIDFILE="+pidFile,
	)
	out, runErr := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		t.Fatalf("run harness: %v\n%s", runErr, out)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("harness did not report a grandchild pid (harness output below):\n%s\nerr: %v", out, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse pidfile %q: %v", pidBytes, err)
	}

	// Pdeathsig delivery is asynchronous; poll for death rather than
	// asserting immediately.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL) // don't leak this test's own reproduction
	t.Fatalf("grandchild pid %d still alive 5s after harness hard-exited with no cleanup", pid)
}

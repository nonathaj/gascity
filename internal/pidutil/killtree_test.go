package pidutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestKillTreeIsANoOpForPidsThatCannotNameAProcess pins the guard clause. KillTree is called
// from cleanup paths where the pid may be zero or unset, and on Unix a non-positive pid passed
// to kill(2) addresses a process GROUP or every process the caller may signal — so treating
// these as ordinary input would be catastrophic rather than merely wrong.
func TestKillTreeIsANoOpForPidsThatCannotNameAProcess(t *testing.T) {
	for _, pid := range []int{0, -1, -1000} {
		if err := KillTree(pid); err != nil {
			t.Fatalf("KillTree(%d) = %v, want nil", pid, err)
		}
	}
}

// TestKillTreeSucceedsForAProcessThatAlreadyExited pins that "already gone" is success. The
// contract is "make sure this is not running", and a process that exited satisfies it; returning
// an error would make every cleanup path log noise for the normal case.
func TestKillTreeSucceedsForAProcessThatAlreadyExited(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sh: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()

	if err := KillTree(pid); err != nil {
		t.Fatalf("KillTree(%d) for an exited process = %v, want nil", pid, err)
	}
}

// TestKillTreeKillsTheProcessItNames is the basic guarantee. It deliberately uses a shell that
// traps TERM and never exits on its own, so a pass cannot be explained by the process having
// finished by itself — the failure mode that made an earlier stop test pass for the wrong reason.
func TestKillTreeKillsTheProcessItNames(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	script := "trap '' TERM; printf ready > " + strconv.Quote(filepath.ToSlash(readyPath)) +
		"; while :; do sleep 5; done"
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sh: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(readyPath)
		if err == nil && strings.TrimSpace(string(raw)) == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell never signalled ready, so this test cannot prove a kill happened")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !Alive(pid) {
		t.Fatalf("pid %d not alive before KillTree, so a later death proves nothing", pid)
	}

	if err := KillTree(pid); err != nil {
		t.Fatalf("KillTree(%d) = %v, want nil", pid, err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive after KillTree; note the shell traps TERM, so only a "+
				"forced kill ends it", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

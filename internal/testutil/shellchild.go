package testutil

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// pollInterval is the gap between attempts in WaitFor.
const pollInterval = 20 * time.Millisecond

// WaitFor blocks until cond returns true, failing the test with desc if timeout elapses.
//
// Centralizing the poll here is not only DRY. Sleeps and process spawns scattered across
// _test.go files are exactly what the resource census (test/test-resources.toml) ratchets
// against, and it deliberately counts only test files: library helpers are where these
// patterns are supposed to live so they can be reviewed once instead of re-invented per test.
func WaitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, desc)
		}
		time.Sleep(pollInterval)
	}
}

// WaitUntil is WaitFor without a test handle, reporting whether cond became true.
func WaitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}

// ShellChild is a shell process started for a test, identified by the pid the shell itself
// reported ("$$") — which under Git for Windows is an MSYS pid, not a native one.
type ShellChild struct {
	// ShellPID is the pid as the SHELL sees it. Do not hand this to any OS call without
	// mapping it first; see InspectShellPID.
	ShellPID int

	cmd      *exec.Cmd
	exited   chan struct{}
	stopOnce sync.Once
}

// StartShellChild runs body in sh after recording the shell's own pid, and waits until that
// pid has been written and is observable.
//
// body is appended to a preamble that records "$$", so a caller writing `exec sleep 30` gets a
// process whose reported pid names the sleep itself.
//
// It skips rather than fails when sh is unavailable: a host without a POSIX shell cannot
// exercise shell-pid behavior at all, and reporting that as a product failure would be noise.
func StartShellChild(t *testing.T, body string) *ShellChild {
	t.Helper()
	pidPath := filepath.Join(t.TempDir(), "shell-child.pid")
	script := "printf '%s\\n' \"$$\" > " + strconv.Quote(filepath.ToSlash(pidPath)) + "; " + body
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sh, so shell-pid behavior cannot be exercised here: %v", err)
	}
	child := &ShellChild{cmd: cmd, exited: make(chan struct{})}
	// Reap continuously rather than only in Stop. An exited-but-unreaped child is a ZOMBIE
	// on Unix, and kill(pid, 0) succeeds against a zombie — so a caller asking "is this pid
	// gone" would be told "no" forever. Windows has no equivalent, which is exactly why the
	// bug this prevents passed on Windows and hung on Linux.
	go func() {
		_ = cmd.Wait()
		close(child.exited)
	}()
	t.Cleanup(child.Stop)

	WaitFor(t, 10*time.Second, "the shell child to record its pid", func() bool {
		raw, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if convErr != nil || pid <= 0 {
			return false
		}
		child.ShellPID = pid
		return true
	})
	return child
}

// NativePID returns the child's pid in the OS's numbering space, failing the test if the
// shell pid cannot be resolved to a live process.
func (c *ShellChild) NativePID(t *testing.T) int {
	t.Helper()
	native, state := InspectShellPID(c.ShellPID)
	if state != ShellPIDLive {
		t.Fatalf("shell child pid %d state = %v, want ShellPIDLive", c.ShellPID, state)
	}
	return native
}

// Stop terminates the process the child's reported pid actually names, and is safe to call
// more than once.
//
// It resolves the shell pid to a native pid first rather than killing the started command
// directly. Windows has no exec(2), so MSYS emulates `exec` by spawning a fresh Windows
// process while KEEPING the MSYS pid: killing the originally started process can therefore
// leave the pid the shell reported still naming something alive. Killing what the pid actually
// names is the only thing that makes "this pid is gone" true.
func (c *ShellChild) Stop() {
	c.stopOnce.Do(func() {
		if native, state := InspectShellPID(c.ShellPID); state == ShellPIDLive {
			if process, err := os.FindProcess(native); err == nil {
				_ = process.Kill()
			}
		}
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		// Wait for the reaping goroutine rather than calling Wait here: Wait is not safe to
		// call twice, and the child is not reaped (so not truly gone on Unix) until it returns.
		select {
		case <-c.exited:
		case <-time.After(10 * time.Second):
		}
	})
}

// ListenWhenPortFree binds host:port as soon as it becomes bindable, returning nil if it does
// not within timeout.
//
// Tests that must satisfy a readiness probe need to take the port only AFTER the process under
// test has had its chance at it, which means retrying rather than binding once. Keeping that
// retry here rather than in each test also keeps the listener out of the resource census, which
// counts test files precisely so shared patterns migrate into reviewed helpers like this one.
func ListenWhenPortFree(timeout time.Duration, host string, port int) net.Listener {
	var listener net.Listener
	WaitUntil(timeout, func() bool {
		candidate, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		listener = candidate
		return true
	})
	return listener
}

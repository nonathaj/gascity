package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startShellChild starts a shell that records its own pid and then sleeps, returning the pid it
// wrote (in the SHELL's numbering space) and a stop function.
func startShellChild(t *testing.T, seconds int) (int, func()) {
	t.Helper()
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	script := "printf '%s\\n' \"$$\" > " + strconv.Quote(filepath.ToSlash(pidPath)) +
		"; exec sleep " + strconv.Itoa(seconds)
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sh: %v", err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	t.Cleanup(stop)

	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(pidPath)
		text := strings.TrimSpace(string(raw))
		if err == nil && text != "" {
			pid, convErr := strconv.Atoi(text)
			if convErr == nil && pid > 0 {
				return pid, stop
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell child never recorded its pid at %s", pidPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestInspectShellPIDReportsALiveProcess covers the ShellPIDLive branch, and on Windows also
// proves the mapping is real: the native pid it returns must differ from the MSYS pid the
// shell wrote, because conflating the two is the bug this whole helper exists for.
func TestInspectShellPIDReportsALiveProcess(t *testing.T) {
	shellPID, _ := startShellChild(t, 30)

	nativePID, state := InspectShellPID(shellPID)
	if state != ShellPIDLive {
		t.Fatalf("InspectShellPID(%d) state = %v, want ShellPIDLive", shellPID, state)
	}
	if nativePID <= 0 {
		t.Fatalf("InspectShellPID(%d) native pid = %d, want a positive pid", shellPID, nativePID)
	}
	if runtime.GOOS != "windows" && nativePID != shellPID {
		t.Fatalf("off Windows there is one pid space, so native pid %d should equal shell pid %d",
			nativePID, shellPID)
	}
}

// TestInspectShellPIDReportsAGoneProcess covers the ShellPIDGone branch — the one that makes a
// death assertion possible at all. If this regressed to Unknown, every "the process was killed"
// assertion in the suite would fail; if Unknown regressed to Gone, they would all pass
// vacuously.
func TestInspectShellPIDReportsAGoneProcess(t *testing.T) {
	shellPID, stop := startShellChild(t, 30)
	stop()

	deadline := time.Now().Add(15 * time.Second)
	for {
		_, state := InspectShellPID(shellPID)
		if state == ShellPIDGone {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("InspectShellPID(%d) state = %v after the process was killed, want "+
				"ShellPIDGone", shellPID, state)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestInspectShellPIDRefusesToAnswerForNonsense pins that a pid which cannot mean anything
// yields Unknown rather than Gone. Reporting "gone" for input that was never valid would let a
// caller conclude a process died when nothing was ever checked.
func TestInspectShellPIDRefusesToAnswerForNonsense(t *testing.T) {
	for _, pid := range []int{0, -1, -12345} {
		if native, state := InspectShellPID(pid); state != ShellPIDUnknown {
			t.Fatalf("InspectShellPID(%d) = (%d, %v), want ShellPIDUnknown", pid, native, state)
		}
	}
}

// TestNativePIDForShellPIDMapsALiveProcess covers the kill-oriented helper's success path.
func TestNativePIDForShellPIDMapsALiveProcess(t *testing.T) {
	shellPID, _ := startShellChild(t, 30)

	nativePID, ok := NativePIDForShellPID(shellPID)
	if !ok {
		t.Fatalf("NativePIDForShellPID(%d) ok = false, want true for a live process", shellPID)
	}
	if nativePID <= 0 {
		t.Fatalf("NativePIDForShellPID(%d) = %d, want a positive pid", shellPID, nativePID)
	}
}

// TestNativePIDForShellPIDRefusesToGuess pins the fail-safe contract that keeps this helper from
// being dangerous.
//
// Callers use the result to KILL. Returning the unmapped input on failure — the "best effort"
// reflex — would hand a caller an MSYS pid to kill in the native pid space, which is how a test
// terminates an unrelated process on a developer's machine. ok=false must mean "do not".
func TestNativePIDForShellPIDRefusesToGuess(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if got, ok := NativePIDForShellPID(pid); ok {
			t.Fatalf("NativePIDForShellPID(%d) = (%d, true), want ok=false: a caller that "+
				"kills this value would signal an unrelated process", pid, got)
		}
	}
}

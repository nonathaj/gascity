package testutil

import (
	"runtime"
	"testing"
	"time"
)

// TestInspectShellPIDReportsALiveProcess covers the ShellPIDLive branch, and on Windows also
// proves the mapping is real: the native pid it returns must differ from the MSYS pid the
// shell wrote, because conflating the two is the bug this helper exists for.
func TestInspectShellPIDReportsALiveProcess(t *testing.T) {
	child := StartShellChild(t, "exec sleep 30")

	nativePID, state := InspectShellPID(child.ShellPID)
	if state != ShellPIDLive {
		t.Fatalf("InspectShellPID(%d) state = %v, want ShellPIDLive", child.ShellPID, state)
	}
	if nativePID <= 0 {
		t.Fatalf("InspectShellPID(%d) native pid = %d, want a positive pid", child.ShellPID, nativePID)
	}
	if runtime.GOOS != "windows" && nativePID != child.ShellPID {
		t.Fatalf("off Windows there is one pid space, so native pid %d should equal shell pid %d",
			nativePID, child.ShellPID)
	}
}

// TestInspectShellPIDReportsAGoneProcess covers the ShellPIDGone branch — the one that makes a
// death assertion possible at all. If this regressed to Unknown, every "the process was killed"
// assertion in the suite would fail; if Unknown regressed to Gone, they would all pass
// VACUOUSLY.
//
// The child is stopped through ShellChild.Stop, which kills what the reported pid actually
// names. An earlier version killed the started command instead and failed on Windows for a
// reason worth recording: Windows has no exec(2), so MSYS emulates `exec` by spawning a new
// Windows process while KEEPING the MSYS pid. Killing the originally started process left the
// reported pid still naming a live `sleep`, and InspectShellPID was right to say Live.
func TestInspectShellPIDReportsAGoneProcess(t *testing.T) {
	child := StartShellChild(t, "exec sleep 30")
	if _, state := InspectShellPID(child.ShellPID); state != ShellPIDLive {
		t.Fatalf("child pid %d was not live before being stopped, so its later absence "+
			"would prove nothing", child.ShellPID)
	}

	child.Stop()

	WaitFor(t, 15*time.Second, "the stopped child to leave the process table", func() bool {
		_, state := InspectShellPID(child.ShellPID)
		return state == ShellPIDGone
	})
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
	child := StartShellChild(t, "exec sleep 30")

	nativePID, ok := NativePIDForShellPID(child.ShellPID)
	if !ok {
		t.Fatalf("NativePIDForShellPID(%d) ok = false, want true for a live process", child.ShellPID)
	}
	if nativePID <= 0 {
		t.Fatalf("NativePIDForShellPID(%d) = %d, want a positive pid", child.ShellPID, nativePID)
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

// Package processgrouptest provides test helpers for subprocess cleanup tests.
package processgrouptest

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

// RequireRealProcessSignals skips tests that intentionally send OS signals
// unless the process-backed test lane explicitly opted in.
func RequireRealProcessSignals(t testing.TB) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("GC_REAL_PROCESS_SIGNAL_TESTS")) == "1" {
		return
	}
	if strings.TrimSpace(os.Getenv("GC_FAST_UNIT")) == "0" {
		return
	}
	t.Skip("skipping real process signal test in unit lane; set GC_FAST_UNIT=0 or GC_REAL_PROCESS_SIGNAL_TESTS=1")
}

// KillFromPIDFile terminates the process whose PID is recorded at path.
func KillFromPIDFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read child pid file %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid file %s: %v", path, err)
	}
	if pid <= 1 {
		return
	}
	// The pid in these files is written by a POSIX shell ("$$"/"$!"), which under Git for
	// Windows is an MSYS pid — a different numbering space from the one os.FindProcess
	// and Kill operate on. Killing it raw does not just fail to reap the intended child:
	// when the number collides with a live native pid it TERMINATES AN UNRELATED PROCESS
	// on the developer's machine, and observed MSYS values sit squarely in the range
	// Windows assigns.
	//
	// Map first, and if the mapping cannot be established, kill NOTHING. Leaking a bounded
	// test child is a far smaller cost than killing something that was never ours. Off
	// Windows this is identity, so behavior there is unchanged.
	nativePID, ok := testutil.NativePIDForShellPID(pid)
	if !ok {
		t.Logf("skipping kill of pid %d from %s: could not map it to a native pid, and "+
			"killing an unmapped shell pid risks terminating an unrelated process", pid, path)
		return
	}
	process, err := os.FindProcess(nativePID)
	if err != nil {
		t.Fatalf("find child process %d from %s: %v", nativePID, path, err)
	}
	_ = process.Kill()
}

// KillObservationTimeout is the run budget to give a command whose child must be
// demonstrably running before the timeout under test fires.
//
// It cannot be tuned to the timeout being tested; it has to clear process startup on the
// slowest supported platform. At 100ms these tests failed on Windows because spawning sh and
// its background subshell takes longer than that, so the child was killed before its first
// write and the test could not tell "killed" from "never started". Linux spawns in ~1ms,
// which is why 100ms looked sufficient for years.
const KillObservationTimeout = 2 * time.Second

// KillDeadline is how long after a runner returns the child may take to actually die.
// Generous on purpose: it bounds a failure, so overshooting costs nothing on the passing
// path, while a tight value would turn scheduling noise into a red test.
const KillDeadline = 10 * time.Second

// AssertProcessFromPIDFileDies fails unless the process recorded at pidPath is gone within
// the given window.
//
// This asserts the subject directly. The alternative — watching a heartbeat file stop
// growing — infers death from the absence of writes, which cannot distinguish "killed" from
// "never started", and needs a stability window longer than the child's write period to mean
// anything. On Windows each loop iteration of such a child costs ~200ms because `sleep` is a
// process spawn, so a short window can be satisfied by a process that is still very much
// alive. Liveness has an answer; there is no reason to guess at it.
//
// The pid is mapped out of the shell's numbering space first (see
// testutil.NativePIDForShellPID). A mapping failure fails the test rather than passing
// quietly: this function exists to prove a process died, and "I could not tell" is not that.
func AssertProcessFromPIDFileDies(t *testing.T, pidPath string, within time.Duration) {
	t.Helper()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read child pid file %s: %v. The child never recorded its pid, so this test "+
			"cannot tell whether the process group was killed or never started", pidPath, err)
	}
	shellPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid file %s: %v", pidPath, err)
	}
	deadline := time.Now().Add(within)
	for {
		nativePID, state := testutil.InspectShellPID(shellPID)
		switch state {
		case testutil.ShellPIDGone:
			// Absent from a readable process table is the answer, not a missing answer.
			return
		case testutil.ShellPIDLive:
			if !pidutil.Alive(nativePID) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("child pid %d (shell pid %d) still alive %s after the runner "+
					"returned; the process group was not killed", nativePID, shellPID, within)
			}
		default:
			t.Fatalf("could not read the process table, so the liveness of child pid %d "+
				"cannot be established. Passing here would make this assertion vacuous on "+
				"any host where the check itself is broken", shellPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// AssertFileSizeStable fails if path keeps growing during stableFor.
//
// stableFor must be a comfortable multiple of the writer's cadence. Too small a
// multiple fails silently rather than loudly: a live writer descheduled for one
// window looks exactly like a dead one, so the assertion passes and quietly
// stops guarding whatever it was pointed at.
//
// The give-up deadline is derived from stableFor rather than fixed, because a
// fixed one silently caps the usable window — with a 3s deadline, asking for 3s
// of stability makes the pass check race the deadline check and a genuinely
// dead writer fails.
func AssertFileSizeStable(t *testing.T, path string, initialSize int64, stableFor time.Duration) {
	t.Helper()
	lastSize := initialSize
	stableSince := time.Now()
	deadline := time.Now().Add(stableFor + 3*time.Second)
	for {
		time.Sleep(50 * time.Millisecond)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat heartbeat file %s: %v", path, err)
		}
		if size := info.Size(); size != lastSize {
			lastSize = size
			stableSince = time.Now()
		}
		if time.Since(stableSince) >= stableFor {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat file %s kept growing after timeout cleanup; latest size %d", path, lastSize)
		}
	}
}

// WaitForFileSize waits until path exists with non-empty contents.
func WaitForFileSize(t *testing.T, path string) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		info, err := os.Stat(path)
		if err == nil {
			if info.Size() > 0 {
				return info.Size()
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat heartbeat file %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for heartbeat file %s to grow", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

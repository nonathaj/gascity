package pidutil_test

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

// TestKillTreeIsANoOpForPidsThatCannotNameAProcess pins the guard clause. KillTree is called
// from cleanup paths where the pid may be zero or unset, and on Unix a non-positive pid passed
// to kill(2) addresses a process GROUP or a broadcast — so treating these as ordinary input is
// catastrophic rather than merely wrong.
func TestKillTreeIsANoOpForPidsThatCannotNameAProcess(t *testing.T) {
	for _, pid := range []int{0, -1, -1000} {
		if err := pidutil.KillTree(pid); err != nil {
			t.Fatalf("KillTree(%d) = %v, want nil", pid, err)
		}
	}
}

// TestKillTreeSucceedsForAProcessThatAlreadyExited pins that "already gone" is success. The
// contract is "make sure this is not running", and a process that exited satisfies it;
// returning an error would make every cleanup path log noise for the normal case.
func TestKillTreeSucceedsForAProcessThatAlreadyExited(t *testing.T) {
	// The native pid is captured while the child is still alive, then the child is stopped
	// and reaped. Resolving it afterwards is not an option and guessing is worse: an exited
	// pid cannot be mapped, and falling back to the raw SHELL pid would hand KillTree a
	// number from the wrong space — which on Unix it signals as a process GROUP. That is the
	// collateral-damage failure this package exists to prevent, so the test must not commit
	// it either.
	child := testutil.StartShellChild(t, "exec sleep 30")
	native := child.NativePID(t)

	child.Stop()
	testutil.WaitFor(t, 15*time.Second, "the stopped child to be reaped", func() bool {
		_, state := testutil.InspectShellPID(child.ShellPID)
		return state == testutil.ShellPIDGone
	})

	if err := pidutil.KillTree(native); err != nil {
		t.Fatalf("KillTree(%d) for an exited process = %v, want nil", native, err)
	}
}

// TestKillTreeKillsTheProcessItNames is the basic guarantee.
//
// The child traps TERM and loops forever, so a pass cannot be explained by the process having
// finished on its own — the failure mode that made an earlier stop test in this thread pass for
// the wrong reason. Only a forced kill ends it.
func TestKillTreeKillsTheProcessItNames(t *testing.T) {
	child := testutil.StartShellChild(t, "trap '' TERM; while :; do sleep 5; done")
	native := child.NativePID(t)
	if !pidutil.Alive(native) {
		t.Fatalf("pid %d not alive before KillTree, so a later death proves nothing", native)
	}

	if err := pidutil.KillTree(native); err != nil {
		t.Fatalf("KillTree(%d) = %v, want nil", native, err)
	}
	testutil.WaitFor(t, 15*time.Second, "the trapped child to die from a forced kill", func() bool {
		return !pidutil.Alive(native)
	})
}

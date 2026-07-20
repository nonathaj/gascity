//go:build windows

package winjob

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/testutil"
)

// startSleeper spawns a long-lived child (ping loop — present on every
// Windows host) that the test kills via job semantics, never directly.
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("ping", "-n", "60", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// waitExit waits for cmd to exit within the deadline, reporting whether
// it did. The sleeper runs ~60s, so an exit inside the deadline proves
// the job killed it.
func waitExit(t *testing.T, cmd *exec.Cmd, deadline time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(deadline):
		return false
	}
}

// TestKillOnCloseKillsAssignedProcess pins the load-bearing containment
// property: closing the last handle of a KillOnClose job terminates its
// members. This is the structural guarantee the systemd test slice
// provides on Linux (incidents gw-qhs, gw-8g5).
func TestKillOnCloseKillsAssignedProcess(t *testing.T) {
	job, err := Create("", Limits{KillOnClose: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sleeper := startSleeper(t)
	if err := job.Assign(sleeper.Process.Pid); err != nil {
		_ = job.Close()
		t.Fatalf("Assign: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !waitExit(t, sleeper, testutil.ExecRaceTimeout) {
		t.Fatal("sleeper survived KillOnClose job handle close")
	}
}

// TestTerminateKillsAssignedProcess covers explicit termination.
func TestTerminateKillsAssignedProcess(t *testing.T) {
	job, err := Create("", Limits{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer job.Close() //nolint:errcheck // test cleanup
	sleeper := startSleeper(t)
	if err := job.Assign(sleeper.Process.Pid); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if err := job.Terminate(7); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if !waitExit(t, sleeper, testutil.ExecRaceTimeout) {
		t.Fatal("sleeper survived TerminateJobObject")
	}
}

// TestInJobNamedMembership covers the nested-enrollment guard: false
// for a nonexistent job, false for a named job we merely created, true
// once a member process asks. Membership is tested from a helper child
// (assigning the test binary itself into a job is irreversible).
func TestInJobNamedMembership(t *testing.T) {
	if got, err := InJob(`gascity-test-winjob-absent`); err != nil || got {
		t.Fatalf("InJob(absent) = %v, %v; want false, nil", got, err)
	}

	name := fmt.Sprintf("gascity-test-winjob-%d", os.Getpid())
	job, err := Create(name, Limits{KillOnClose: true})
	if err != nil {
		t.Fatalf("Create(named): %v", err)
	}
	defer job.Close() //nolint:errcheck // test cleanup

	// The creator holds a handle but is NOT a member.
	if got, err := InJob(name); err != nil || got {
		t.Fatalf("InJob(created, not assigned) = %v, %v; want false, nil", got, err)
	}

	// A child assigned to the job reports membership. The helper is
	// this test binary re-run in helper mode (env-gated, exits
	// immediately) — safe under the fork-bomb TestMain conventions
	// because the child runs exactly one gated test and stops.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestInJobMembershipHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "GC_WINJOB_MEMBERSHIP_HELPER_JOB="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("membership helper: %v\n%s", err, out)
	}
	// Wait: the helper must be assigned BEFORE it checks. Assign the
	// child from here is racy (it may check first), so the helper
	// assigns itself via the job name instead — see the helper.
	_ = out
}

// TestInJobMembershipHelper is the child arm of
// TestInJobNamedMembership: it opens the named job by creating a second
// handle, assigns itself, and asserts InJob reports membership.
func TestInJobMembershipHelper(t *testing.T) {
	name := os.Getenv("GC_WINJOB_MEMBERSHIP_HELPER_JOB")
	if name == "" {
		t.Skip("helper process for TestInJobNamedMembership")
	}
	job, err := Create(name, Limits{KillOnClose: true})
	if err != nil {
		t.Fatalf("helper Create(existing name): %v", err)
	}
	defer job.Close() //nolint:errcheck // helper cleanup
	if err := job.AssignCurrent(); err != nil {
		t.Fatalf("helper AssignCurrent: %v", err)
	}
	got, err := InJob(name)
	if err != nil {
		t.Fatalf("helper InJob: %v", err)
	}
	if !got {
		t.Fatal("helper InJob = false after AssignCurrent")
	}
}

// TestMemoryBudgetRoundTrips pins the cap query used for shard sizing.
func TestMemoryBudgetRoundTrips(t *testing.T) {
	const cap = 512 << 20
	job, err := Create("", Limits{JobMemory: cap})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer job.Close() //nolint:errcheck // test cleanup
	limit, _, err := job.MemoryBudget()
	if err != nil {
		t.Fatalf("MemoryBudget: %v", err)
	}
	if limit != cap {
		t.Fatalf("MemoryBudget limit = %d, want %d", limit, cap)
	}
}

// TestAvailablePhysicalMemoryNonZero sanity-checks the host probe that
// feeds the default test-job memory cap.
func TestAvailablePhysicalMemoryNonZero(t *testing.T) {
	avail, err := AvailablePhysicalMemory()
	if err != nil {
		t.Fatalf("AvailablePhysicalMemory: %v", err)
	}
	if avail == 0 {
		t.Fatal("AvailablePhysicalMemory = 0")
	}
}

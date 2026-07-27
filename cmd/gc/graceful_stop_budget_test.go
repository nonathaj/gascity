package main

import (
	"strings"
	"testing"
	"time"
)

// runTimedPidSpaceScript runs body with the pack script's prelude sourced and reports how
// long the whole sh invocation took.
func runTimedPidSpaceScript(t *testing.T, body string) (string, time.Duration) {
	t.Helper()
	start := time.Now()
	out := runPidSpaceScript(t, body)
	return out, time.Since(start)
}

// slowProbePrelude replaces pid_alive with a stub that reports "still alive" after a
// deliberate delay.
//
// The delay is what makes this test mean anything off Windows. The real defect is that an
// iteration-counted budget measures POLLS, not TIME, so its wall-clock meaning is set by
// however long one probe happens to take on the host: a measured 124s against an intended
// 30s on Windows, where pid_alive costs ~560ms and each `sleep` is a process spawn. On
// Linux the same code looks correct purely because the probe is cheap. Paying for a slow
// probe explicitly reproduces the stretch on every platform, so this is a real regression
// test on Linux CI rather than a Windows-only one.
func slowProbePrelude(probeSeconds string) string {
	return "pid_alive() { sleep " + probeSeconds + "; return 0; }\n"
}

// TestGracefulStopBudgetIsMeasuredInWallClock is the regression test for the stop budget:
// the wait must last about as long as it says, whatever a single probe costs.
func TestGracefulStopBudgetIsMeasuredInWallClock(t *testing.T) {
	// A 4s budget against a 1s probe. Deadline-based, this exits at ~4-5s. Counting
	// iterations instead spends budget*2 polls x (1s probe + 0.5s sleep) = ~12s, so the
	// two designs are far enough apart that the assertion cannot be satisfied by timing
	// noise in either direction.
	body := slowProbePrelude("1") + `
wait_pid_exit_until 4242 4 && echo UNEXPECTED_EXIT
echo waited
`
	out, elapsed := runTimedPidSpaceScript(t, body)
	if !strings.Contains(out, "waited") {
		t.Fatalf("wait did not run to completion; output:\n%s", out)
	}
	if strings.Contains(out, "UNEXPECTED_EXIT") {
		t.Fatalf("wait reported the process exited, but the probe always said alive; output:\n%s", out)
	}
	if elapsed < 4*time.Second {
		t.Fatalf("wait returned after %s, before its own 4s budget elapsed: a budget that "+
			"expires early force-kills a process that was still shutting down cleanly", elapsed)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("wait took %s for a 4s budget. The budget is being counted in polls rather "+
			"than measured in time, so its real length is whatever one pid_alive costs on this "+
			"host (measured 124s against an intended 30s on Windows)", elapsed)
	}
}

// TestGracefulStopReturnsAsSoonAsTheProcessExits pins the other half of the contract. A
// deadline must not become a floor: the common case is a process that exits promptly, and
// spending the whole budget on it would make every clean stop as slow as the worst case.
func TestGracefulStopReturnsAsSoonAsTheProcessExits(t *testing.T) {
	body := `
probe_calls=0
pid_alive() {
    probe_calls=$((probe_calls + 1))
    [ "$probe_calls" -le 2 ]
}
wait_pid_exit_until 4242 30 || echo BUDGET_EXHAUSTED
echo waited
`
	out, elapsed := runTimedPidSpaceScript(t, body)
	if strings.Contains(out, "BUDGET_EXHAUSTED") {
		t.Fatalf("wait reported budget exhaustion for a process that exited; output:\n%s", out)
	}
	if !strings.Contains(out, "waited") {
		t.Fatalf("wait did not run to completion; output:\n%s", out)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("wait took %s to notice a process that exited on the third probe, against a "+
			"30s budget: the deadline is acting as a floor instead of a ceiling", elapsed)
	}
}

// TestGracefulStopStaysBoundedWithoutAClock covers the degraded path. The wait reads a
// clock per poll, and `date` is a subprocess that can fail; if the loop trusted it
// unconditionally a clock failure would turn a bounded wait into an unbounded spin,
// hanging `stop` forever. It must fall back to counting polls instead — a stretched
// budget is recoverable, a hang is not.
func TestGracefulStopStaysBoundedWithoutAClock(t *testing.T) {
	body := slowProbePrelude("0.2") + `
clock_seconds() { return 1; }
wait_pid_exit_until 4242 2 && echo UNEXPECTED_EXIT
echo waited
`
	out, elapsed := runTimedPidSpaceScript(t, body)
	if !strings.Contains(out, "waited") {
		t.Fatalf("wait did not run to completion without a clock; output:\n%s", out)
	}
	if strings.Contains(out, "UNEXPECTED_EXIT") {
		t.Fatalf("wait reported an exit the probe never signalled; output:\n%s", out)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("wait took %s with no clock available: the fallback is not bounding the "+
			"loop, so a failing `date` hangs stop indefinitely", elapsed)
	}
}

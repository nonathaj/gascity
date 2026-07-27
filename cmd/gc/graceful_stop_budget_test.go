package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// countingProbePrelude replaces pid_alive with a stub that always reports "still alive",
// costs probeSeconds per call, and counts how many times it was asked.
//
// The count is the load-proof discriminator between the two budget designs, and it is why
// this test does not assert on wall-clock duration. An iteration-counted budget always
// spends exactly budget*2 polls, whatever a probe costs. A measured budget spends however
// many polls fit in the time, so an expensive probe means FEW polls. Timing the wait
// instead was flaky on Windows for a reason no tolerance can fix: every poll spawns `sleep`
// and `date`, and process creation there is slow and unbounded under load, so a 4s budget
// was observed taking anywhere from 4s to 25s while behaving correctly.
func countingProbePrelude(probeSeconds string) string {
	return `probe_calls=0
pid_alive() { probe_calls=$((probe_calls + 1)); sleep ` + probeSeconds + `; return 0; }
`
}

// runBudgetScript runs a wait_pid_exit_until call with the prelude sourced and reports how
// many times the probe was asked and how many seconds the WAIT itself took.
//
// Elapsed is measured inside the shell, bracketing only the call under test: timing the
// whole invocation from Go also counts shell startup and parsing the ~3500-line prelude,
// which is seconds of variance that has nothing to do with the budget.
func runBudgetScript(t *testing.T, probeBody, waitCall string) (probes, elapsed int, out string) {
	t.Helper()
	body := probeBody + `
wait_start=$(date +%s)
` + waitCall + `
wait_end=$(date +%s)
printf 'probes=%s elapsed=%s\n' "$probe_calls" "$((wait_end - wait_start))"
echo waited
`
	out = runPidSpaceScript(t, body)
	if !strings.Contains(out, "waited") {
		t.Fatalf("wait did not run to completion; output:\n%s", out)
	}
	match := regexp.MustCompile(`probes=(\d+) elapsed=(\d+)`).FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("script did not report probe count and elapsed; output:\n%s", out)
	}
	probes, _ = strconv.Atoi(match[1])
	elapsed, _ = strconv.Atoi(match[2])
	return probes, elapsed, out
}

// TestGracefulStopBudgetIsMeasuredInWallClock is the regression test for the stop budget:
// the wait must last about as long as it says, whatever a single probe costs.
func TestGracefulStopBudgetIsMeasuredInWallClock(t *testing.T) {
	// A 4s budget against a 2s probe. Two independent assertions, each robust to load:
	//
	//   - elapsed >= 4: the budget is not cut short. Load only ever makes this larger, so it
	//     cannot flake.
	//   - probes <= 5: the wait adapted to what a probe cost. At 2s per probe only two or
	//     three fit in 4s, and a slower host fits FEWER. An iteration-counted budget spends
	//     exactly budget*2 = 8 every time, so it cannot satisfy this bound on any host.
	//
	// There is deliberately no ceiling on elapsed. That was the flaky assertion, and no
	// tolerance fixes it: each poll spawns sleep and date, and Windows process creation is
	// slow enough under load that a correct wait was measured at 25s for a 4s budget.
	probes, elapsed, out := runBudgetScript(t, countingProbePrelude("2"),
		"wait_pid_exit_until 4242 4 && echo UNEXPECTED_EXIT")
	if strings.Contains(out, "UNEXPECTED_EXIT") {
		t.Fatalf("wait reported the process exited, but the probe always said alive; output:\n%s", out)
	}
	if elapsed < 4 {
		t.Fatalf("wait returned after %ds, before its own 4s budget elapsed: a budget that "+
			"expires early force-kills a process that was still shutting down cleanly", elapsed)
	}
	if probes > 5 {
		t.Fatalf("wait made %d probes for a 4s budget against a 2s probe; a measured budget "+
			"fits only two or three, while counting iterations always spends budget*2 = 8. "+
			"The budget's real length is whatever one pid_alive costs on this host (measured "+
			"124s against an intended 30s on Windows)", probes)
	}
}

// TestGracefulStopReturnsAsSoonAsTheProcessExits pins the other half of the contract. A
// deadline must not become a floor: the common case is a process that exits promptly, and
// spending the whole budget on it would make every clean stop as slow as the worst case.
func TestGracefulStopReturnsAsSoonAsTheProcessExits(t *testing.T) {
	probe := `
probe_calls=0
pid_alive() {
    probe_calls=$((probe_calls + 1))
    [ "$probe_calls" -le 2 ]
}
`
	probes, _, out := runBudgetScript(t, probe, "wait_pid_exit_until 4242 30 || echo BUDGET_EXHAUSTED")
	if strings.Contains(out, "BUDGET_EXHAUSTED") {
		t.Fatalf("wait reported budget exhaustion for a process that exited; output:\n%s", out)
	}
	// Asserted in probes rather than seconds: the process "exits" on the third probe, so a
	// wait that returns as soon as it notices asks exactly three times. Spending the 30s
	// budget would show up as 60.
	if probes != 3 {
		t.Fatalf("wait made %d probes for a process that exited on the third; the deadline is "+
			"acting as a floor instead of a ceiling", probes)
	}
}

// TestGracefulStopStaysBoundedWithoutAClock covers the degraded path. The wait reads a
// clock per poll, and `date` is a subprocess that can fail; if the loop trusted it
// unconditionally a clock failure would turn a bounded wait into an unbounded spin,
// hanging `stop` forever. It must fall back to counting polls instead — a stretched
// budget is recoverable, a hang is not.
func TestGracefulStopStaysBoundedWithoutAClock(t *testing.T) {
	probe := countingProbePrelude("0.2") + "clock_seconds() { return 1; }\n"
	probes, _, out := runBudgetScript(t, probe, "wait_pid_exit_until 4242 2 && echo UNEXPECTED_EXIT")
	if strings.Contains(out, "UNEXPECTED_EXIT") {
		t.Fatalf("wait reported an exit the probe never signaled; output:\n%s", out)
	}
	// With no clock the poll counter is the only bound, at budget*2 = 4. Asserting the exact
	// count proves the fallback engaged rather than the loop merely finishing quickly: a
	// spin would never stop, and a still-working clock would give a different number.
	if probes != 4 {
		t.Fatalf("wait made %d probes with no clock available, want exactly budget*2 = 4; the "+
			"fallback is not bounding the loop, so a failing `date` hangs stop indefinitely", probes)
	}
}

package testutil

import (
	"fmt"
	"os"
	"time"
)

// WatchdogDeadline is the hard upper bound on a single test binary's
// lifetime enforced by RunWithWatchdog. It must comfortably exceed the
// slowest legitimate package run and stay below any external reaper's
// stale-process threshold (30 minutes on the Windows dev hosts).
const WatchdogDeadline = 25 * time.Minute

// testMainRunner matches *testing.M without importing the testing
// package, which would leak testing flags into non-test builds.
type testMainRunner interface{ Run() int }

// RunWithWatchdog runs a package's tests under a hard-deadline watchdog
// and never returns; call it from TestMain as the function's only
// statement. The watchdog force-exits the test binary via os.Exit after
// WatchdogDeadline, bypassing hung Cleanups and blocked subprocess Waits.
//
// This exists because Windows does not tear down process trees: when a
// `go test` invocation (or the agent driving it) is killed, the *.test
// binary survives, and one blocked in cmd.Wait on a stdin-blocked child
// lives forever, pinning its commit charge. The Go test -timeout panic
// does not reliably end such a binary; os.Exit does. (Incident gw-qhs:
// 1,583 orphaned session.test.exe processes, ~93 GB of commit.)
func RunWithWatchdog(m testMainRunner) {
	StartExitWatchdog()
	os.Exit(m.Run())
}

// StartExitWatchdog arms the hard-deadline watchdog without taking over
// TestMain. Use it at the top of a TestMain that needs its own run/exit
// structure (testscript re-execution, post-run cleanup).
func StartExitWatchdog() {
	go func() {
		time.Sleep(WatchdogDeadline)
		fmt.Fprintf(os.Stderr, "testutil watchdog: test binary exceeded the %v hard deadline; forcing exit to avoid an orphaned test process\n", WatchdogDeadline)
		os.Exit(2)
	}()
}

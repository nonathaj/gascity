package testutil

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// watchdogFloor is the minimum hard bound on a test binary's lifetime
// enforced by the exit watchdog. It must comfortably exceed the slowest
// legitimate default-timeout package run and stay below any external
// reaper's stale-process threshold (30 minutes on the Windows dev
// hosts). Runs given a larger -test.timeout get a larger watchdog
// deadline; see watchdogDeadline.
const watchdogFloor = 25 * time.Minute

// watchdogSlack is added on top of -test.timeout so the Go test
// framework's own timeout panic (which prints goroutine dumps) always
// fires first; the watchdog is the backstop for binaries the panic
// cannot end.
const watchdogSlack = 2 * time.Minute

// WatchdogDeadlineEnv overrides the watchdog deadline with a
// time.Duration string (e.g. "2h", "90s"). "0" disarms the watchdog
// entirely — for interactive debugging runs with -test.timeout=0.
const WatchdogDeadlineEnv = "GC_TEST_WATCHDOG_DEADLINE"

// testMainRunner matches *testing.M without importing the testing
// package into this non-test file.
type testMainRunner interface{ Run() int }

// watchdogDeadline resolves the effective deadline from the override
// env, the -test.timeout flag in args, and the floor. A zero return
// means "disarmed". Exposed to args for testability.
func watchdogDeadline(env string, args []string) time.Duration {
	if env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			return d
		}
	}
	deadline := watchdogFloor
	if t, ok := testTimeoutFromArgs(args); ok {
		if t == 0 {
			// -test.timeout=0 is a deliberate "no timeout" debugging
			// run; the watchdog must not undercut it.
			return 0
		}
		if t+watchdogSlack > deadline {
			deadline = t + watchdogSlack
		}
	}
	return deadline
}

// testTimeoutFromArgs extracts the -test.timeout (or -timeout) flag
// value from a test binary's argv.
func testTimeoutFromArgs(args []string) (time.Duration, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var val string
		switch {
		case strings.HasPrefix(arg, "-test.timeout="):
			val = strings.TrimPrefix(arg, "-test.timeout=")
		case strings.HasPrefix(arg, "--test.timeout="):
			val = strings.TrimPrefix(arg, "--test.timeout=")
		case arg == "-test.timeout" || arg == "--test.timeout":
			if i+1 < len(args) {
				val = args[i+1]
			}
		default:
			continue
		}
		if d, err := time.ParseDuration(val); err == nil {
			return d, true
		}
		return 0, false
	}
	return 0, false
}

// RunWithWatchdog runs a package's tests under a hard-deadline watchdog
// and never returns; call it from TestMain as the function's only
// statement. The watchdog force-exits the test binary via os.Exit,
// bypassing hung Cleanups and blocked subprocess Waits.
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
//
// The deadline is max(25m, -test.timeout + 2m), so the framework's own
// timeout panic always fires first on runs with a large -test.timeout.
// GC_TEST_WATCHDOG_DEADLINE overrides it; "0" (or -test.timeout=0
// without an override) disarms the watchdog for debugging runs.
func StartExitWatchdog() {
	deadline := watchdogDeadline(os.Getenv(WatchdogDeadlineEnv), os.Args[1:])
	if deadline <= 0 {
		return
	}
	go func() {
		time.Sleep(deadline)
		fmt.Fprintf(os.Stderr, "testutil watchdog: test binary exceeded the %v hard deadline; forcing exit to avoid an orphaned test process\n", deadline)
		os.Exit(2)
	}()
}

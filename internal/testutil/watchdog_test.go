package testutil

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWatchdogDeadline(t *testing.T) {
	cases := []struct {
		name string
		env  string
		args []string
		want time.Duration
	}{
		{name: "default floor", env: "", args: nil, want: watchdogFloor},
		{name: "small timeout keeps floor", env: "", args: []string{"-test.timeout=10m"}, want: watchdogFloor},
		{name: "large timeout extends", env: "", args: []string{"-test.timeout=60m"}, want: 60*time.Minute + watchdogSlack},
		{name: "large timeout separate arg", env: "", args: []string{"-test.timeout", "60m"}, want: 60*time.Minute + watchdogSlack},
		{name: "double-dash form", env: "", args: []string{"--test.timeout=45m"}, want: 45*time.Minute + watchdogSlack},
		{name: "timeout zero disarms", env: "", args: []string{"-test.timeout=0"}, want: 0},
		{name: "timeout zero with unit disarms", env: "", args: []string{"-test.timeout=0s"}, want: 0},
		{name: "env override wins", env: "90s", args: []string{"-test.timeout=60m"}, want: 90 * time.Second},
		{name: "env zero disarms", env: "0", args: nil, want: 0},
		{name: "invalid env ignored", env: "not-a-duration", args: nil, want: watchdogFloor},
		{name: "invalid timeout ignored", env: "", args: []string{"-test.timeout=bogus"}, want: watchdogFloor},
		{name: "unrelated args ignored", env: "", args: []string{"-test.run=TestX", "-test.v=true"}, want: watchdogFloor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := watchdogDeadline(tc.env, tc.args); got != tc.want {
				t.Fatalf("watchdogDeadline(%q, %v) = %v, want %v", tc.env, tc.args, got, tc.want)
			}
		})
	}
}

// TestWatchdogHelperHang is not a real test: it is the hung-binary
// stand-in for TestStartExitWatchdogEndsHungBinary, entered only when
// re-executed with the helper env set. It arms the watchdog with a
// short env-overridden deadline and then blocks well past it.
func TestWatchdogHelperHang(t *testing.T) {
	if os.Getenv("GC_TEST_WATCHDOG_HELPER") != "1" {
		t.Skip("helper process for TestStartExitWatchdogEndsHungBinary")
	}
	StartExitWatchdog()
	time.Sleep(30 * time.Second)
}

func TestStartExitWatchdogEndsHungBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestWatchdogHelperHang", "-test.timeout=25s")
	cmd.Env = append(os.Environ(),
		"GC_TEST_WATCHDOG_HELPER=1",
		WatchdogDeadlineEnv+"=500ms",
	)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("hung helper exited cleanly in %v; watchdog did not fire:\n%s", elapsed, out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v (not an exit error):\n%s", err, out)
	}
	if code := exitErr.ExitCode(); code != 2 {
		t.Fatalf("helper exit code = %d, want 2 (watchdog):\n%s", code, out)
	}
	if elapsed >= 25*time.Second {
		t.Fatalf("helper took %v; the framework timeout beat the watchdog", elapsed)
	}
}

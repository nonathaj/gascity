//go:build windows

package testutil

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/winjob"
	"golang.org/x/sys/windows"
)

// TestTestBinaryTreeIsContained proves the automatic containment
// contract: every test binary in this repo blank-imports
// internal/testenv (lint-enforced), whose init places the binary in a
// kill-on-close Job Object on Windows. No wrapper script, no caller
// discipline — if this binary is not in such a job, the containment
// regressed and killed test runs orphan their trees again (incidents
// gw-qhs, gw-8g5).
func TestTestBinaryTreeIsContained(t *testing.T) {
	flags, err := winjob.CurrentJobLimitFlags()
	if err != nil {
		t.Fatalf("CurrentJobLimitFlags (is testenv's containTestProcessTree wired?): %v", err)
	}
	if flags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatalf("test binary's job lacks KILL_ON_JOB_CLOSE (flags %#x); containment regressed", flags)
	}
}

// TestContainmentHelperSpawnAndHang is the env-gated child arm of the
// manual/e2e containment check: it spawns a long-lived grandchild and
// hangs, so an external harness can kill THIS process alone and assert
// the grandchild dies with it via the job.
func TestContainmentHelperSpawnAndHang(t *testing.T) {
	if os.Getenv("GC_TEST_CONTAINMENT_HANG_HELPER") != "1" {
		t.Skip("helper process for external containment verification")
	}
	cmd := exec.Command("ping", "-n", "60", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start grandchild: %v", err)
	}
	// Report the grandchild PID for the external harness, then hang.
	t.Logf("GRANDCHILD_PID=%d", cmd.Process.Pid)
	time.Sleep(50 * time.Second)
}

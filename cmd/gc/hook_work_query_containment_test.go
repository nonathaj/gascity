package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestShellWorkQueryKillsBackgroundedGrandchildOnTimeout pins containment for the
// hook work-query path.
//
// shellWorkQueryWithEnv runs a pack-authored shell command. Such a command may
// background its work ("cmd &"), and on Windows the resulting grandchild is not
// reachable from the direct child's process tree once the timeout fires — killing
// sh.exe leaves it running forever. That is the gw-591 mechanism; this asserts the
// work-query path is contained the same way the provider op and probe now are.
//
// Structure note: the child pid must be mapped from MSYS to Windows space WHILE the
// process is alive (waitForProviderTestChildPID uses `ps -W`, which only lists live
// processes), so the query runs in a goroutine and the pid is captured before the
// timeout fires rather than after.
func TestShellWorkQueryKillsBackgroundedGrandchildOnTimeout(t *testing.T) {
	prevTimeout := hookWorkQueryTimeout
	hookWorkQueryTimeout = 2 * time.Second
	t.Cleanup(func() { hookWorkQueryTimeout = prevTimeout })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	// shScriptPath: slash-form and single-quoted so the temp path survives sh
	// (doctrine P8).
	query := "sh -c 'echo $$ > " + shScriptPath(pidFile) + "; while :; do sleep 1; done' &\nwait\n"

	resultCh := make(chan error, 1)
	go func() {
		_, err := shellWorkQueryWithEnv(query, dir, nil)
		resultCh <- err
	}()

	pid := waitForProviderTestChildPID(t, pidFile)

	select {
	case <-resultCh:
	case <-time.After(20 * time.Second):
		t.Fatal("shellWorkQueryWithEnv did not return after its timeout")
	}

	waitForProviderTestPIDExit(t, pid, "hook work query")
}

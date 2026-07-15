//go:build !windows

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// startStandaloneBdDoltLikeProcess fakes a standalone dolt sql-server via
// bash `exec -a` reading a FIFO — Unix process-identity machinery with no
// Windows analog, so the Windows arm skips the tests that need it.
func startStandaloneBdDoltLikeProcess(t *testing.T, dataDir string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires bash for exec -a")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(dataDir): %v", err)
	}
	fifo := filepath.Join(dataDir, "sql-server")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo(sql-server): %v", err)
	}
	cmd := exec.Command("bash", "-c", `exec -a dolt cat sql-server -- --data-dir "$1"`, "fake-dolt", dataDir)
	cmd.Dir = dataDir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start fake dolt sql-server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	// 30s: must outlast a worst-case processArgsPSTimeout (10s) ps fallback plus
	// process-exec/proc-reflection latency under heavy parallel CI load.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.Process.Signal(syscall.Signal(0)) == nil && processLooksLikeDoltSQLServer(cmd.Process.Pid, dataDir) {
			return cmd
		}
		time.Sleep(10 * time.Millisecond)
	}
	args, _ := processArgs(cmd.Process.Pid)
	t.Fatalf("fake dolt sql-server did not become inspectable; pid=%d args=%q", cmd.Process.Pid, args)
	return cmd
}

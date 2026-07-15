//go:build !windows

package main

// These tests exercise Unix-only process machinery: inherited pipe FDs
// passed by number to the managed-dolt test watchdog (syscall.Dup), and
// POSIX process-group signal semantics (Setpgid/Getpgid, kill(-pgid)).
// The Windows managed-dolt arm routes through processgroup/taskkill and is
// covered by the platform seams' own tests.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/processgroup/processgrouptest"
)

func TestManagedDoltTestParentDoneClosesOnPipeEOF(t *testing.T) {
	parentPipeRead, parentPipeWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer parentPipeRead.Close() //nolint:errcheck
	parentPipeFD, err := syscall.Dup(int(parentPipeRead.Fd()))
	if err != nil {
		t.Fatalf("dup parent pipe fd: %v", err)
	}
	done, closeDone, err := managedDoltTestParentDone(strconv.Itoa(parentPipeFD))
	if err != nil {
		_ = syscall.Close(parentPipeFD)
		t.Fatalf("managedDoltTestParentDone: %v", err)
	}
	defer closeDone()

	if err := parentPipeWrite.Close(); err != nil {
		t.Fatalf("close parent pipe writer: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parent pipe EOF did not close done channel")
	}
}

func TestManagedDoltWatchdogParentPipeEOFHonorsDisarm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	withManagedDoltTestMode(t, false)
	t.Setenv(managedDoltTestModeEnv, "1")
	fakeDoltDir := writeFakeDoltSQLServer(t)
	t.Setenv("PATH", fakeDoltDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	configPath := filepath.Join(dir, "dolt-config.yaml")
	logPath := filepath.Join(dir, "dolt.log")
	disarmFile := filepath.Join(dir, "watchdog.disarm")
	if err := os.WriteFile(configPath, []byte("log_level: debug\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(disarmFile, []byte("ready\n"), 0o644); err != nil {
		t.Fatalf("write disarm file: %v", err)
	}
	parentPipeRead, parentPipeWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("create parent pipe: %v", err)
	}
	defer parentPipeRead.Close()  //nolint:errcheck
	defer parentPipeWrite.Close() //nolint:errcheck
	watchdogParentPipeFD, err := syscall.Dup(int(parentPipeRead.Fd()))
	if err != nil {
		t.Fatalf("dup parent pipe fd for watchdog: %v", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = syscall.Close(watchdogParentPipeFD)
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer stdoutRead.Close()  //nolint:errcheck
	defer stdoutWrite.Close() //nolint:errcheck
	stderrPath := filepath.Join(dir, "watchdog.stderr")
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = syscall.Close(watchdogParentPipeFD)
		t.Fatalf("open stderr file: %v", err)
	}
	defer stderrFile.Close() //nolint:errcheck

	result := make(chan int, 1)
	args := []string{strconv.Itoa(os.Getpid()), configPath, logPath, disarmFile, strconv.Itoa(watchdogParentPipeFD)}
	go func() {
		result <- runManagedDoltTestWatchdog(args, stdoutWrite, stderrFile)
	}()

	doltPID, err := readManagedDoltTestWatchdogPID(stdoutRead, os.Getpid())
	if err != nil {
		t.Fatalf("read fake dolt pid: %v", err)
	}
	t.Cleanup(func() { cleanupManagedDoltTestPID(t, doltPID) })
	if err := parentPipeWrite.Close(); err != nil {
		t.Fatalf("close parent pipe writer: %v", err)
	}
	select {
	case code := <-result:
		if code != 0 {
			stderrData, _ := os.ReadFile(stderrPath)
			t.Fatalf("watchdog exit code = %d, want 0; stderr:\n%s", code, stderrData)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not exit after disarm file and parent pipe EOF")
	}
	if !pidAlive(doltPID) {
		t.Fatalf("fake dolt pid %d exited; disarm file should win over parent pipe EOF", doltPID)
	}
	if _, err := os.Stat(disarmFile); !os.IsNotExist(err) {
		t.Fatalf("disarm file still exists after watchdog exit: %v", err)
	}
}

// TestTerminateManagedDoltTestPIDKillsProcessGroup is the #2313 follow-up M3
// regression: when the target is a process-group leader, terminate must
// signal the whole group so descendant dolt workers do not survive.
// Demonstration: spawn a shell as group leader, fork a backgrounded sleep
// child, call terminateManagedDoltTestPID on the shell. Both must die.
// Without the M3 fix (leader-only kill), the child outlives the shell.
func TestTerminateManagedDoltTestPIDKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group signal semantics required")
	}
	processgrouptest.RequireRealProcessSignals(t)
	dir := t.TempDir()
	childFile := filepath.Join(dir, "child.pid")
	// Shell becomes the new process group leader (Setpgid:true). It forks
	// a backgrounded sleep that inherits that group, records the child's
	// PID, then waits.
	cmd := exec.Command("/bin/sh", "-c", `sleep 90 & echo $! > "$1"; wait`, "sh", childFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start shell: %v", err)
	}
	shellPID := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Wait for the child PID to be recorded.
	var childPID int
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(childFile)
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
				childPID = pid
				break
			}
		}
	}
	if childPID == 0 {
		t.Fatalf("child sleep never recorded its PID at %s", childFile)
	}

	if err := terminateManagedDoltTestPID(shellPID); err != nil {
		t.Fatalf("terminateManagedDoltTestPID(%d): %v", shellPID, err)
	}

	// Allow a short window for the kernel to mark both pids dead.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(shellPID) && !pidAlive(childPID) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pidAlive(shellPID) {
		t.Errorf("shell pid %d still alive after pgid terminate", shellPID)
	}
	if pidAlive(childPID) {
		t.Errorf("child pid %d still alive after pgid terminate; M3 pgid-kill regression", childPID)
	}
}

// TestTerminateManagedDoltTestPIDLeaderOnlyForNonGroupLeader asserts the
// safety guard added in M3: when the target is NOT its own pgid leader (e.g.
// the watchdog inheriting the test binary's group), terminate must NOT
// signal the whole group — that would take down the test binary. We pick a
// child of the test binary that did NOT call Setpgid; it inherits the test
// binary's group. Terminate must only kill the child.
func TestTerminateManagedDoltTestPIDLeaderOnlyForNonGroupLeader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group signal semantics required")
	}
	// Spawn a sleep WITHOUT Setpgid — it inherits the test binary's pgid.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", pid, err)
	}
	if pgid == pid {
		t.Skip("sleep happens to be its own group leader; cannot exercise leader-only fallback")
	}

	if err := terminateManagedDoltTestPID(pid); err != nil {
		t.Fatalf("terminateManagedDoltTestPID(%d): %v", pid, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for pidAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pidAlive(pid) {
		t.Errorf("sleep pid %d still alive after terminate", pid)
	}
	// Sanity: the test binary itself is still alive (we did not pgid-kill
	// our own group). If we had, the test process would have died and this
	// assertion would never run — but if it did, this guards against a
	// future regression where the fallback path forgets the leader check.
	if !pidAlive(os.Getpid()) {
		t.Fatalf("test binary signaled by terminate fallback; pgid safety check failed")
	}
}

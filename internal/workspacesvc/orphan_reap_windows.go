//go:build windows

package workspacesvc

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// signalProcessOrGroup terminates the process tree rooted at pid — the
// Windows analogue of signaling a Unix process group. SIGKILL forces the
// kill; any other signal attempts taskkill's graceful tree termination.
// Unsafe targets are refused outright.
func signalProcessOrGroup(pid int, sig syscall.Signal) {
	if unsafeSignalTarget(pid) {
		log.Printf("workspacesvc: refusing to signal unsafe orphan-reap target pid %d", pid)
		return
	}
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if sig == syscall.SIGKILL {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = kill.Run()
}

// unsafeSignalTarget reports whether pid must never be signaled: system
// pids (idle/system), nonpositive pids, and the sweeper's own process.
// This mirrors processgroup.Terminate's refusal so kill-adjacent code stays
// safe under future refactors.
func unsafeSignalTarget(pid int) bool {
	return pid <= 4 || pid == os.Getpid()
}

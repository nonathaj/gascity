//go:build !windows

package dolttest

import (
	"os"
	"syscall"
)

// signalPID delivers sig to pid. On Unix this is a direct syscall.Kill.
func signalPID(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// processExists reports whether pid is live. Signal 0 probes existence without
// delivering a signal; EPERM means the process exists but is not ours to
// signal — treat as alive (don't reap).
func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// reraiseSignal re-delivers ss to this process after the handler has reset the
// signal's default disposition, so an interrupted or timed-out run exits with
// the expected signal semantics.
func reraiseSignal(ss syscall.Signal) {
	_ = syscall.Kill(os.Getpid(), ss)
}

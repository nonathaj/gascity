//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// pidGone reports whether the given pid no longer represents a live
// process — either the entry has been reaped (ESRCH on signal-zero)
// or it has exited and is awaiting wait() from its parent (zombie).
// Both cases mean the process can no longer hold ports or files, so
// the supervisor restart can safely proceed.
//
// We probe via signal-zero first because it covers both "PID never
// existed" and "PID was reaped" without an extra /proc syscall. The
// /proc/<pid>/status fallback handles the zombie case that signal
// zero reports as alive.
func pidGone(pid int) bool {
	if err := syscall.Kill(pid, syscall.Signal(0)); err == syscall.ESRCH {
		return true
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		// If /proc/<pid>/status is missing, the kernel has already
		// torn down the entry — ESRCH-equivalent.
		return os.IsNotExist(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "State:") {
			continue
		}
		// State lines look like "State:\tZ (zombie)" or "State:\tR
		// (running)" — a zombie has already released its ports and
		// FDs even though the parent has not reaped it.
		return strings.Contains(line, "Z")
	}
	return false
}

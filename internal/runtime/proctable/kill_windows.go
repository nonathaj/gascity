//go:build windows

package proctable

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/runtime"
)

// KillByPID terminates pid's process tree: a graceful taskkill first, a
// forced one after runtime.ManagedProcessStopGrace, then a bounded wait
// (runtime.ManagedProcessReapGrace) for the process to be confirmed dead
// before returning — approximating the Unix SIGTERM→SIGKILL→reap contract so
// callers can refuse to start a name-reused replacement that would race a
// survivor. Already-gone processes are success.
//
// Residual difference from the Unix path: the Unix reaper captures the
// target's start-time identity before signaling and confirms death against
// it, so a PID recycled within the reap window is not mistaken for a live
// survivor. pidutil.StartTime is unsupported on Windows, so liveness here is
// plain pidutil.Alive; a PID reused inside ManagedProcessReapGrace could read
// alive and make KillByPID report "not confirmed dead". This is low
// probability (Windows recycles PIDs slowly and a real kill sets a non-259
// exit code that confirms death promptly) and unfixable without a Windows
// start-time source.
func KillByPID(pid int) error {
	return killByPIDWith(pid, taskkillTree, pidutil.Alive,
		runtime.ManagedProcessStopGrace, runtime.ManagedProcessReapGrace)
}

// killByPIDWith is the kill/confirm core with its process operations
// injected so the confirmed-dead-before-return contract is unit-testable
// without real processes.
func killByPIDWith(
	pid int,
	kill func(pid int, force bool) error,
	alive func(int) bool,
	grace, reapGrace time.Duration,
) error {
	if pid <= 1 {
		return fmt.Errorf("proctable: refusing to kill PID %d", pid)
	}
	if !alive(pid) {
		return nil
	}
	// Graceful tree kill. taskkill without /F fails for console processes
	// with no window or message pump; that is expected — the forced pass
	// below is the real teeth, so the error is intentionally ignored.
	_ = kill(pid, false)
	if waitUntil(func() bool { return !alive(pid) }, grace) {
		return nil
	}
	if err := kill(pid, true); err != nil && alive(pid) {
		return fmt.Errorf("proctable: force-kill PID %d: %w", pid, err)
	}
	if waitUntil(func() bool { return !alive(pid) }, reapGrace) {
		return nil
	}
	return fmt.Errorf("proctable: PID %d still alive %s after forced kill (not confirmed dead)", pid, reapGrace)
}

// taskkillTree terminates the process tree rooted at pid. force adds /F.
func taskkillTree(pid int, force bool) error {
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}

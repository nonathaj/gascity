//go:build windows

package proctable

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

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
	// Snapshot the descendant PIDs BEFORE any kill. taskkill /T walks the
	// live parent tree, but the graceful pass can kill the root first; a
	// backgrounded grandchild (e.g. `sleep &` under sh) is then orphaned with
	// a dead parent PID and falls out of the tree, so the forced /T pass can
	// no longer reach it — Windows has no process-group kill like Unix. The
	// captured set lets killByPIDWith force-kill any such survivors directly.
	descendants := descendantPIDs(pid)
	kill := func(root int, force bool) error {
		err := taskkillTree(root, force)
		// Force-sweep captured descendants on BOTH passes: the graceful tree
		// kill can terminate the root (breaking the tree) while a backgrounded
		// grandchild survives, and killByPIDWith would then see the root dead
		// and return success without a forced pass. Stop means the whole
		// managed session dies, so orphaned descendants are always force-killed.
		for _, d := range descendants {
			if pidutil.Alive(d) {
				_ = taskkillTree(d, true)
			}
		}
		return err
	}
	return killByPIDWith(pid, kill, pidutil.Alive,
		runtime.ManagedProcessStopGrace, runtime.ManagedProcessReapGrace)
}

// descendantPIDs returns the transitive child PIDs of root, snapshotted from
// the process table. Best-effort: a snapshot failure yields nil.
func descendantPIDs(root int) []int {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // read-only handle

	children := map[int][]int{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil
	}
	for {
		children[int(entry.ParentProcessID)] = append(children[int(entry.ParentProcessID)], int(entry.ProcessID))
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	var out []int
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			// ParentProcessID is not cleared when a parent dies, so PIDs can
			// alias into cycles across recycling; the seen set bounds the walk.
			if c > 1 && !seen[c] {
				seen[c] = true
				out = append(out, c)
				queue = append(queue, c)
			}
		}
	}
	return out
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

//go:build !windows

package pidutil

import (
	"errors"
	"os"
	"syscall"
)

// KillTree terminates pid and every process descended from it.
//
// The Unix counterpart of the Windows taskkill /T walk. Here the durable grouping is the
// process group: signalling the negative pid reaches every member, including descendants that
// have been orphaned and re-parented, which is precisely what a tree walk cannot do once an
// intermediate parent exits.
//
// The group signal is attempted first and the process itself is signalled as a fallback,
// because a pid is only a group leader if it was spawned with Setpgid. ESRCH is not an error
// for a function whose contract is "make sure this is gone" — a process that already exited
// satisfies that.
func KillTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}

//go:build !windows

package runtime

import (
	"fmt"
	"os"
	"syscall"
)

// narrowPrivateDir enforces the owner-only contract with Unix mode bits: the
// directory must belong to this process's effective uid, and anything wider
// than privateDirMode is chmod'ed back down.
func narrowPrivateDir(path string, info os.FileInfo, euid int) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private directory %q has unsupported ownership metadata", path)
	}
	if got, want := stat.Uid, uint32(euid); got != want {
		return fmt.Errorf("private directory %q is owned by uid %d, want %d", path, got, want)
	}

	// Chmod unconditionally when anything is off, including the special bits: a
	// setgid directory silently propagates group ownership to everything written
	// beneath it, which defeats the point of the 0700.
	special := info.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if info.Mode().Perm() != privateDirMode || special != 0 {
		if err := os.Chmod(path, privateDirMode); err != nil {
			return fmt.Errorf("tightening private directory %q: %w", path, err)
		}
	}
	return nil
}

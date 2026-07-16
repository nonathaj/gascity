//go:build !windows

package subprocess

import (
	"fmt"
	"os"
	"syscall"
)

// validatePrivateSocketOwnership enforces the Unix contract for a per-user
// private socket directory: mode exactly 0700, no setuid/setgid/sticky bits,
// and ownership by the expected euid.
func validatePrivateSocketOwnership(path string, info os.FileInfo, euid int) error {
	if got := info.Mode().Perm(); got != 0o700 {
		return fmt.Errorf("private socket directory %q has mode %04o, want 0700", path, got)
	}
	if special := info.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky); special != 0 {
		return fmt.Errorf("private socket directory %q has special mode bits %v", path, special)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private socket directory %q has unsupported ownership metadata", path)
	}
	if got, want := stat.Uid, uint32(euid); got != want {
		return fmt.Errorf("private socket directory %q is owned by uid %d, want %d", path, got, want)
	}
	return nil
}

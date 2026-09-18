//go:build !windows

package runtime

import (
	"fmt"
	"os"
	"syscall"
)

// privateDirOwnedBy reports an error unless the directory described by info is
// owned by euid. Foreign ownership cannot be repaired by a chmod, so callers
// fail closed on it.
func privateDirOwnedBy(path string, info os.FileInfo, euid int) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private directory %q has unsupported ownership metadata", path)
	}
	if got, want := stat.Uid, uint32(euid); got != want {
		return fmt.Errorf("private directory %q is owned by uid %d, want %d", path, got, want)
	}
	return nil
}

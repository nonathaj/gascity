//go:build !windows

package fsys

import "os"

// readFileSharing is plain os.ReadFile off Windows: a POSIX rename is already atomic
// with respect to readers, and an open reader keeps its inode, so no special share
// mode is needed to let a concurrent replace proceed.
func readFileSharing(name string) ([]byte, error) {
	return os.ReadFile(name)
}

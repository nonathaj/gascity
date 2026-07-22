//go:build !windows

package fsys

import (
	"errors"
	"syscall"
)

// isRetryableDirRemoveError reports whether an os.Remove(dir) failure may clear
// on its own. POSIX has no delete-pending state, so a non-empty directory is
// genuinely shared and never worth retrying.
func isRetryableDirRemoveError(error) bool {
	return false
}

// isDirNotEmpty reports whether err is POSIX ENOTEMPTY.
func isDirNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY)
}

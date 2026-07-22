//go:build windows

package fsys

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isRetryableDirRemoveError reports whether an os.Remove(dir) failure may clear
// on its own: the transient sharing class, or ERROR_DIR_NOT_EMPTY raised while
// the directory's last child is still delete-pending after its handle dropped.
func isRetryableDirRemoveError(err error) bool {
	return isTransientRenameError(err) || errors.Is(err, windows.ERROR_DIR_NOT_EMPTY)
}

// isDirNotEmpty reports whether err is Windows' ERROR_DIR_NOT_EMPTY (145),
// which os.Remove returns for a non-empty directory instead of ENOTEMPTY.
func isDirNotEmpty(err error) bool {
	return errors.Is(err, windows.ERROR_DIR_NOT_EMPTY)
}

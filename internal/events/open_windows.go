//go:build windows

package events

import (
	"os"

	"golang.org/x/sys/windows"
)

// openEventLog opens the append-mode event log with FILE_SHARE_DELETE so the
// live log can be renamed (rotation) or unlinked (cleanup, TempDir teardown)
// while this recorder still holds it open. Go's os.OpenFile omits
// FILE_SHARE_DELETE, so a plain handle blocks rotation's rename and every
// deletion on Windows with ERROR_SHARING_VIOLATION. FILE_APPEND_DATA gives
// kernel-level append; GENERIC_READ is required because LockFileEx (the
// cross-process flock) needs read or write access on the handle.
func openEventLog(path string) (*os.File, error) {
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		pathp,
		windows.FILE_APPEND_DATA|windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
